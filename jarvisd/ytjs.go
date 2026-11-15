package main

// Расшифровка ссылок YouTube. Клиент «телевизор» (TVHTML5) отдаёт дорожку целиком без особого токена,
// но ссылки у него зашифрованы: подпись (s) и параметр n надо пропустить через функции из скрипта плеера.
// Делаем как yt-dlp (проект yt-dlp/ejs, Unlicense): из плеера выбрасываем вызовы с побочными эффектами,
// находим функцию, которая собирает ссылку (в её теле есть x.set("alr","yes")), и выполняем плеер в goja.
// Разбор плеера — парсером goja на Go, так быстрее, чем гонять JS-парсер внутри JS.
// Плеер весит ~3 МБ и меняется раз в несколько дней; готовая машина держится в памяти, пока играет
// музыка, и выбрасывается через неделю без реального использования.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"reflect"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/token"
)

type ytJS struct {
	mu     sync.Mutex
	id     string // версия плеера
	idAt   time.Time
	sts    int
	vm     *goja.Runtime
	solve  []goja.Callable
	used   time.Time
	reaper *time.Timer
	failID string // плеер, который не разобрался: не качаем его снова каждую песню
	failAt time.Time
}

var (
	ytPlayerIDRe = regexp.MustCompile(`player\\?/([0-9a-zA-Z_-]{8})\\?/`)
	ytStsRe      = regexp.MustCompile(`signatureTimestamp[=:](\d+)`)
)

const ytJSIdle = 7 * 24 * time.Hour // разбор на колонке ~11 с — держим готовым (~15 МБ), пока плеер не сменится

// playerID — текущая версия плеера (раз в час спрашиваем у YouTube).
func (j *ytJS) playerID(ctx context.Context, client *http.Client) (string, error) {
	if j.id != "" && time.Since(j.idAt) < time.Hour {
		return j.id, nil
	}
	body, err := ytGet(ctx, client, "https://www.youtube.com/iframe_api", 1<<20)
	if err != nil {
		if j.id != "" {
			return j.id, nil
		}
		return "", err
	}
	m := ytPlayerIDRe.FindSubmatch(body)
	if m == nil {
		return "", errors.New("не нашлась версия плеера YouTube")
	}
	j.id, j.idAt = string(m[1]), time.Now()
	return j.id, nil
}

func ytGet(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("YouTube ответил %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// prepare — убедиться, что плеер загружен; возвращает signatureTimestamp для запроса /player.
func (j *ytJS) prepare(ctx context.Context, client *http.Client) (int, error) {
	return j.prepareWithUse(ctx, client, true)
}

func (j *ytJS) prepareWithUse(ctx context.Context, client *http.Client, realUse bool) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if !realUse && !j.used.IsZero() && time.Since(j.used) >= ytJSIdle {
		return j.sts, nil // periodic checks must not reload an unused VM after its expiry
	}
	id, err := j.playerID(ctx, client)
	if err != nil {
		return 0, err
	}
	if j.vm != nil && j.idLoaded() == id {
		if realUse {
			j.touch()
		}
		return j.sts, nil
	}
	if j.failID == id && time.Since(j.failAt) < 30*time.Minute {
		return 0, errors.New("плеер YouTube " + id + " не разбирается")
	}
	t0 := time.Now()
	src, err := ytGet(ctx, client, "https://www.youtube.com/s/player/"+id+"/player_ias.vflset/en_US/base.js", 16<<20)
	if err != nil {
		return 0, fmt.Errorf("плеер YouTube: %w", err)
	}
	if m := ytStsRe.FindSubmatch(src); m != nil {
		j.sts, _ = strconv.Atoi(string(m[1]))
	}
	tDl := time.Since(t0)
	j.vm, j.solve = nil, nil // старый плеер больше не нужен — не держим два сразу
	vm, solve, err := ytBuildSolver(string(src))
	src = nil
	debug.FreeOSMemory() // разбор занимал десятки мегабайт — отдаём их системе
	if err != nil {
		j.failID, j.failAt = id, time.Now()
		return 0, fmt.Errorf("плеер YouTube %s: %w", id, err)
	}
	j.vm, j.solve = vm, solve
	j.vm.Set("__player", id)
	if realUse || j.used.IsZero() {
		j.touch()
	}
	log.Printf("музыка: плеер YouTube %s готов (скачан за %.1f с, разобран за %.1f с)", id, tDl.Seconds(), (time.Since(t0) - tDl).Seconds())
	return j.sts, nil
}

func (j *ytJS) idLoaded() string {
	if j.vm == nil {
		return ""
	}
	return j.vm.Get("__player").String()
}

// touch — отложить выбрасывание машины.
func (j *ytJS) touch() {
	j.used = time.Now()
	if j.reaper == nil {
		j.reaper = time.AfterFunc(ytJSIdle, j.reap)
	} else {
		j.reaper.Reset(ytJSIdle)
	}
}

func (j *ytJS) reap() {
	j.mu.Lock()
	if time.Since(j.used) < ytJSIdle-time.Second || j.vm == nil {
		j.mu.Unlock()
		return
	}
	j.vm, j.solve = nil, nil
	j.mu.Unlock()
	debug.FreeOSMemory()
	log.Printf("музыка: плеер YouTube выгружен из памяти")
}

// decipher — расшифровать подпись и n. Пустые на входе остаются пустыми.
func (j *ytJS) decipher(sig, n string) (string, string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.vm == nil {
		return "", "", errors.New("плеер YouTube не загружен")
	}
	j.touch()
	var outSig, outN string
	var errs []string
	got := false
	for _, f := range j.solve {
		arg := j.vm.NewObject()
		if sig != "" {
			_ = arg.Set("sig", sig)
		}
		if n != "" {
			_ = arg.Set("n", n)
		}
		v, err := f(goja.Undefined(), arg)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		o := v.ToObject(j.vm)
		s, nn := jsStr(o.Get("sig")), jsStr(o.Get("n"))
		if (sig != "" && s == "") || (n != "" && (nn == "" || nn == n)) {
			errs = append(errs, "пустой ответ")
			continue
		}
		if got && (s != outSig || nn != outN) {
			return "", "", errors.New("функции плеера дают разные ответы")
		}
		outSig, outN, got = s, nn, true
	}
	if !got {
		return "", "", fmt.Errorf("не расшифровалось: %s", strings.Join(errs, "; "))
	}
	return outSig, outN, nil
}

func jsStr(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	return v.String()
}

const ytJSSetup = `if (typeof globalThis.XMLHttpRequest === "undefined") { globalThis.XMLHttpRequest = { prototype: {} }; }
globalThis.location = { hash: "", host: "www.youtube.com", hostname: "www.youtube.com", href: "https://www.youtube.com/watch?v=yt-dlp-wins",
  origin: "https://www.youtube.com", password: "", pathname: "/watch", port: "", protocol: "https:", search: "?v=yt-dlp-wins", username: "" };
if (typeof globalThis.document === "undefined") { globalThis.document = Object.create(null); }
if (typeof globalThis.navigator === "undefined") { globalThis.navigator = Object.create(null); }
if (typeof globalThis.self === "undefined") { globalThis.self = globalThis; }
if (typeof globalThis.window === "undefined") { globalThis.window = globalThis; }
`

const ytJSSolver = `(({sig, n}) => {
  const url = (%s)("https://youtube.com/watch?v=yt-dlp-wins", "s", sig ? encodeURIComponent(sig) : undefined);
  url.set("n", n);
  const proto = Object.getPrototypeOf(url);
  const keys = Object.keys(proto).concat(Object.getOwnPropertyNames(proto));
  for (const key of keys) {
    if (!["constructor", "set", "get", "clone"].includes(key)) { url[key](); break; }
  }
  const s = url.get("s");
  return { sig: s ? decodeURIComponent(s) : null, n: url.get("n") ?? null };
})`

// jsDefs — какие имена объявляет оператор верхнего уровня плеера. Свойства параметра-пространства
// имён (g.Foo = …) считаем отдельными именами «g.Foo».
func jsDefs(st ast.Statement, param string) []string {
	var out []string
	switch s := st.(type) {
	case *ast.VariableStatement:
		for _, d := range s.List {
			if id, ok := d.Target.(*ast.Identifier); ok {
				out = append(out, string(id.Name))
			}
		}
	case *ast.LexicalDeclaration:
		for _, d := range s.List {
			if id, ok := d.Target.(*ast.Identifier); ok {
				out = append(out, string(id.Name))
			}
		}
	case *ast.FunctionDeclaration:
		if s.Function != nil && s.Function.Name != nil {
			out = append(out, string(s.Function.Name.Name))
		}
	case *ast.ClassDeclaration:
		if s.Class != nil && s.Class.Name != nil {
			out = append(out, string(s.Class.Name.Name))
		}
	case *ast.ExpressionStatement:
		e := s.Expression
		for {
			a, ok := e.(*ast.AssignExpression)
			if !ok {
				break
			}
			if d := jsRoot(a.Left, param); d != "" {
				out = append(out, d)
			}
			e = a.Right
		}
	}
	return out
}

// jsRoot — корень цели присваивания: a.b.c → a; g.Foo.bar → g.Foo.
func jsRoot(e ast.Expression, param string) string {
	for {
		switch x := e.(type) {
		case *ast.Identifier:
			return string(x.Name)
		case *ast.DotExpression:
			if id, ok := x.Left.(*ast.Identifier); ok && param != "" && string(id.Name) == param {
				return param + "." + string(x.Identifier.Name)
			}
			e = x.Left
		case *ast.BracketExpression:
			e = x.Left
		default:
			return ""
		}
	}
}

// jsUses — имена верхнего уровня, на которые ссылается оператор. Параметры и переменные вложенных
// функций отсекаются (в сжатом коде они совпадают с короткими именами верхнего уровня); при сомнении
// имя считается используемым — лишнее оставить безопасно, нужное выкинуть нельзя.
func jsUses(n any, param string, out map[string]bool) {
	var scopes []map[string]bool
	local := func(name string) bool {
		for i := len(scopes) - 1; i >= 0; i-- {
			if scopes[i][name] {
				return true
			}
		}
		return false
	}
	var visit func(v reflect.Value)
	enter := func(params *ast.ParameterList, decls []*ast.VariableDeclaration, body ast.Node) {
		sc := map[string]bool{}
		if params != nil {
			for _, b := range params.List {
				if id, ok := b.Target.(*ast.Identifier); ok {
					sc[string(id.Name)] = true
				}
			}
			if id, ok := params.Rest.(*ast.Identifier); ok {
				sc[string(id.Name)] = true
			}
		}
		for _, d := range decls {
			for _, b := range d.List {
				if id, ok := b.Target.(*ast.Identifier); ok {
					sc[string(id.Name)] = true
				}
			}
		}
		if bs, ok := body.(*ast.BlockStatement); ok {
			for _, st := range bs.List {
				switch s := st.(type) {
				case *ast.FunctionDeclaration:
					if s.Function != nil && s.Function.Name != nil {
						sc[string(s.Function.Name.Name)] = true
					}
				case *ast.LexicalDeclaration:
					for _, b := range s.List {
						if id, ok := b.Target.(*ast.Identifier); ok {
							sc[string(id.Name)] = true
						}
					}
				}
			}
		}
		scopes = append(scopes, sc)
		if params != nil {
			visit(reflect.ValueOf(params))
		}
		if body != nil {
			visit(reflect.ValueOf(body))
		}
		scopes = scopes[:len(scopes)-1]
	}
	visit = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			switch x := v.Interface().(type) {
			case *ast.FunctionLiteral:
				// имя функционального выражения видно только внутри неё
				var body ast.Node
				if x.Body != nil {
					body = x.Body
				}
				if x.Name != nil {
					scopes = append(scopes, map[string]bool{string(x.Name.Name): true})
					enter(x.ParameterList, x.DeclarationList, body)
					scopes = scopes[:len(scopes)-1]
				} else {
					enter(x.ParameterList, x.DeclarationList, body)
				}
				return
			case *ast.ArrowFunctionLiteral:
				enter(x.ParameterList, x.DeclarationList, x.Body)
				return
			case *ast.Identifier:
				if !local(string(x.Name)) {
					out[string(x.Name)] = true
				}
				return
			case *ast.DotExpression:
				if id, ok := x.Left.(*ast.Identifier); ok && param != "" && string(id.Name) == param {
					out[param+"."+string(x.Identifier.Name)] = true
					return
				}
				visit(reflect.ValueOf(x.Left)) // имя свойства — не переменная
				return
			}
			visit(v.Elem())
		case reflect.Interface:
			if !v.IsNil() {
				visit(v.Elem())
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if f := v.Field(i); f.CanInterface() {
					visit(f)
				}
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i))
			}
		}
	}
	visit(reflect.ValueOf(n))
}

// ytBuildSolver — подготовить плеер и выполнить его; вернуть кандидатов-расшифровщиков.
func ytBuildSolver(src string) (*goja.Runtime, []goja.Callable, error) {
	// на колонке ~240 МБ памяти: держим пик пониже — сборщик мусора чаще и между шагами
	defer debug.SetGCPercent(debug.SetGCPercent(20))
	chunks, err := ytPreprocess(src)
	if err != nil {
		return nil, nil, err
	}
	runtime.GC()
	vm := goja.New()
	res := vm.NewObject()
	_ = vm.Set("_result", res)
	for i := range chunks {
		if _, err := vm.RunString(chunks[i]); err != nil {
			return nil, nil, fmt.Errorf("выполнение (кусок %d из %d): %w", i+1, len(chunks), err)
		}
		chunks[i] = ""
		if i%4 == 3 {
			runtime.GC()
		}
	}
	arr := res.Get("f").ToObject(vm)
	var solve []goja.Callable
	for i := 0; i < int(arr.Get("length").ToInteger()); i++ {
		if f, ok := goja.AssertFunction(arr.Get(strconv.Itoa(i))); ok {
			solve = append(solve, f)
		}
	}
	return vm, solve, nil
}

// ytSolverCandidates — имена функций, в теле которых есть вызов x.y("alr", "yes").
func ytSolverCandidates(st ast.Statement) []ast.Expression {
	var out []ast.Expression
	check := func(name ast.Expression, fn *ast.FunctionLiteral) {
		if fn == nil || fn.Async || fn.Body == nil || name == nil {
			return
		}
		for _, s := range fn.Body.List {
			if ytIsAlrYes(s) {
				out = append(out, name)
				return
			}
		}
	}
	switch s := st.(type) {
	case *ast.ExpressionStatement:
		if a, ok := s.Expression.(*ast.AssignExpression); ok && a.Operator == token.ASSIGN {
			switch a.Left.(type) {
			case *ast.Identifier, *ast.DotExpression, *ast.BracketExpression:
				if fn, ok := a.Right.(*ast.FunctionLiteral); ok {
					check(a.Left, fn)
				}
			}
		}
	case *ast.FunctionDeclaration:
		if s.Function != nil && s.Function.Name != nil {
			check(s.Function.Name, s.Function)
		}
	case *ast.VariableStatement:
		for _, d := range s.List {
			if fn, ok := d.Initializer.(*ast.FunctionLiteral); ok {
				if id, ok := d.Target.(*ast.Identifier); ok {
					check(id, fn)
				}
			}
		}
	case *ast.LexicalDeclaration:
		for _, d := range s.List {
			if fn, ok := d.Initializer.(*ast.FunctionLiteral); ok {
				if id, ok := d.Target.(*ast.Identifier); ok {
					check(id, fn)
				}
			}
		}
	}
	return out
}

func ytIsAlrYes(s ast.Statement) bool {
	es, ok := s.(*ast.ExpressionStatement)
	if !ok {
		return false
	}
	call, ok := es.Expression.(*ast.CallExpression)
	if !ok || len(call.ArgumentList) != 2 {
		return false
	}
	switch c := call.Callee.(type) {
	case *ast.DotExpression:
		if _, ok := c.Left.(*ast.Identifier); !ok {
			return false
		}
	case *ast.BracketExpression:
		if _, ok := c.Left.(*ast.Identifier); !ok {
			return false
		}
	default:
		return false
	}
	a, ok1 := call.ArgumentList[0].(*ast.StringLiteral)
	b, ok2 := call.ArgumentList[1].(*ast.StringLiteral)
	return ok1 && ok2 && a.Value == "alr" && b.Value == "yes"
}

// stmtStart — начало оператора с открывающими скобками: у «(function(){...})()» парсер ставит начало
// на function, а скобка перед ней — тоже часть оператора.
func stmtStart(src string, s int) int {
	for k := s; k > 0; k-- {
		switch src[k-1] {
		case '(':
			s = k - 1
		case ' ', 9, 10, 13:
		default:
			return s
		}
	}
	return s
}

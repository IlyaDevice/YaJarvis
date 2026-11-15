package main

// Подготовка плеера YouTube к выполнению в goja — экономно по памяти (на колонке ~240 МБ на всё).
// Целиком синтаксическое дерево трёхмегабайтного плеера занимает ~60 МБ, поэтому плеер режется лёгким
// лексером на операторы верхнего уровня, и каждый кусок разбирается настоящим парсером отдельно. Лексер
// может ошибиться (регулярное выражение против деления) — тогда кусок не разберётся и склеится со
// следующим: правильность держит парсер, лексер влияет только на размер кусков.

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
)

// обёртка плеера: «var _yt_player={};(function(g){…})(_yt_player);» или «(function(g){…}).call(this);»
var (
	ytWrapStart = regexp.MustCompile(`^\s*(var\s+[\w$]+\s*=\s*\{\}\s*;\s*)?\(function\(([\w$]*)\)\{`)
	ytWrapEnd   = regexp.MustCompile(`\}\)(\.call\(this(,\s*[\w$]+)?\)|\([\w$]*\))\s*;?\s*$`)
)

// ytPreprocess — плеер без вызовов с побочными эффектами и без того, что не нужно функции ссылки,
// нарезанный на куски для выполнения по очереди на глобальном уровне; последний кусок кладёт
// расшифровщики в _result.f.
func ytPreprocess(src string) ([]string, error) {
	ws := ytWrapStart.FindStringSubmatchIndex(src)
	we := ytWrapEnd.FindStringIndex(src)
	if ws == nil || we == nil || we[0] < ws[1] {
		return nil, errors.New("неожиданное устройство плеера")
	}
	param := src[ws[4]:ws[5]]
	skipFirst := ws[2] >= 0 // как в ejs: во втором виде первый оператор («var window=this») не нужен
	body := src[ws[1]:we[0]]

	type piece struct {
		text  string
		defs  []string
		uses  []int32
		root  bool
		fdecl bool
		vars  []string
	}
	intern := map[string]int32{}
	id := func(s string) int32 {
		if v, ok := intern[s]; ok {
			return v
		}
		v := int32(len(intern))
		intern[s] = v
		return v
	}
	var pieces []piece
	var names []string
	strict, first := false, true
	cuts := jsSplit(body)
	for i, from := 0, 0; i < len(cuts); {
		// кусок от from до cuts[j]; не разобрался — добавляем следующие (по 1, 2, 4… разреза)
		j, step := i, 1
		var prog *ast.Program
		var err error
		for {
			prog, err = parser.ParseFile(nil, "", body[from:cuts[j]], 0)
			if err == nil {
				break
			}
			if j == len(cuts)-1 {
				return nil, fmt.Errorf("разбор: %w", err)
			}
			j = min(j+step, len(cuts)-1)
			step *= 2
		}
		text := body[from:cuts[j]]
		from, i = cuts[j], j+1
		base := prog.File.Base()
		for k, st := range prog.Body {
			a := stmtStart(text, int(st.Idx0())-base)
			b := len(text)
			if k+1 < len(prog.Body) {
				b = stmtStart(text, int(prog.Body[k+1].Idx0())-base)
			}
			if first {
				first = false
				if es, ok := st.(*ast.ExpressionStatement); ok {
					if sl, ok := es.Expression.(*ast.StringLiteral); ok && sl.Value == "use strict" {
						strict = true
					}
				}
				if skipFirst {
					continue
				}
			}
			if es, ok := st.(*ast.ExpressionStatement); ok {
				switch es.Expression.(type) {
				case *ast.AssignExpression, *ast.StringLiteral, *ast.NumberLiteral, *ast.BooleanLiteral, *ast.NullLiteral:
				default:
					continue
				}
			}
			pc := piece{text: text[a:b], defs: jsDefs(st, param)}
			used := map[string]bool{}
			jsUses(st, param, used)
			for u := range used {
				pc.uses = append(pc.uses, id(u))
			}
			pc.root = len(pc.defs) == 0
			if cs := ytSolverCandidates(st); len(cs) > 0 {
				for _, c := range cs {
					names = append(names, text[int(c.Idx0())-base:int(c.Idx1())-base])
				}
				pc.root = true
			}
			switch s := st.(type) {
			case *ast.FunctionDeclaration:
				pc.fdecl = true
			case *ast.VariableStatement:
				for _, d := range s.List {
					if v, ok := d.Target.(*ast.Identifier); ok {
						pc.vars = append(pc.vars, string(v.Name))
					}
				}
			}
			pieces = append(pieces, pc)
		}
	}
	if len(names) == 0 {
		return nil, errors.New("в плеере не нашлась функция ссылки")
	}

	// от функции ссылки — по зависимостям: всё, что объявляет используемые имена
	byName := map[int32][]int{}
	for i, pc := range pieces {
		for _, d := range pc.defs {
			v := id(d)
			byName[v] = append(byName[v], i)
		}
	}
	keep := make([]bool, len(pieces))
	seen := make([]bool, len(intern)+1)
	var queue []int
	for i, pc := range pieces {
		if pc.root {
			keep[i] = true
			queue = append(queue, i)
		}
	}
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		for _, u := range pieces[i].uses {
			if int(u) < len(seen) && seen[u] {
				continue
			}
			if int(u) < len(seen) {
				seen[u] = true
			}
			for _, j := range byName[u] {
				if !keep[j] {
					keep[j] = true
					queue = append(queue, j)
				}
			}
		}
	}

	// Выполняем кусками на глобальном уровне. Чтобы смысл не поменялся: параметр обёртки — глобальный
	// объект, все var объявлены заранее, объявления функций идут первыми (в функции они и так «всплывают»).
	prefix := ""
	if strict {
		prefix = "'use strict';\n"
	}
	chunks := []string{ytJSSetup}
	if param != "" {
		chunks = append(chunks, "var "+param+" = {};")
	}
	var vars []string
	for i, pc := range pieces {
		if keep[i] {
			vars = append(vars, pc.vars...)
		}
	}
	if len(vars) > 0 {
		chunks = append(chunks, "var "+strings.Join(vars, ",")+";")
	}
	add := func(funcs bool) {
		var b strings.Builder
		flush := func() {
			if b.Len() > 0 {
				chunks = append(chunks, prefix+b.String())
				b.Reset()
			}
		}
		for i, pc := range pieces {
			if keep[i] && pc.fdecl == funcs {
				b.WriteString(pc.text)
				b.WriteString(";\n")
				if b.Len() > 256<<10 {
					flush()
				}
			}
		}
		flush()
	}
	add(true)
	add(false)
	var b strings.Builder
	b.WriteString(prefix + "_result.f = [")
	for i, name := range names {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, ytJSSolver, name)
	}
	b.WriteString("];\n")
	chunks = append(chunks, b.String())
	return chunks, nil
}

// jsSplit — места после «;» на верхнем уровне скобок (концы кусков). Последний — конец текста.
func jsSplit(s string) []int {
	var cuts []int
	depth := 0
	var prev byte // последний значимый символ
	word := ""    // последнее слово (return, typeof… — после них «/» начинает регулярку)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			continue
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			if e := strings.Index(s[i+2:], "*/"); e >= 0 {
				i += e + 3
			} else {
				i = len(s)
			}
			continue
		case c == '\'' || c == '"' || c == '`':
			for i++; i < len(s) && s[i] != c; i++ {
				if s[i] == '\\' {
					i++
				}
			}
			prev, word = '"', ""
			continue
		case c == '/' && jsRegexAllowed(prev, word):
			inClass := false
			for i++; i < len(s); i++ {
				ch := s[i]
				if ch == '\\' {
					i++
				} else if ch == '[' {
					inClass = true
				} else if ch == ']' {
					inClass = false
				} else if ch == '/' && !inClass {
					break
				} else if ch == '\n' {
					break
				}
			}
			prev, word = 'r', ""
			continue
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case c == ';' && depth == 0 && !jsContinues(s[i+1:]):
			cuts = append(cuts, i+1)
		}
		if c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			if jsWordChar(prev) {
				word += string(c)
			} else {
				word = string(c)
			}
		} else {
			word = ""
		}
		prev = c
	}
	if len(cuts) == 0 || cuts[len(cuts)-1] != len(s) {
		cuts = append(cuts, len(s))
	}
	return cuts
}

// jsContinues — после «;» идёт продолжение того же оператора: «if(a)b;else c», «do x;while(y)».
func jsContinues(rest string) bool {
	rest = strings.TrimLeft(rest, " \t\r\n")
	for _, w := range []string{"else", "catch", "finally", "while"} {
		if strings.HasPrefix(rest, w) && (len(rest) == len(w) || !jsWordChar(rest[len(w)])) {
			return true
		}
	}
	return false
}

func jsWordChar(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func jsRegexAllowed(prev byte, word string) bool {
	if prev == 0 || strings.IndexByte("(,=:[!&|?{};+-*%<>~^", prev) >= 0 {
		return true
	}
	switch word {
	case "return", "typeof", "case", "do", "else", "in", "of", "new", "delete", "void", "throw", "instanceof", "yield", "await":
		return true
	}
	return false
}

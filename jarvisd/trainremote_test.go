package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeCloud struct {
	mu      sync.Mutex
	n       int
	files   map[string][]byte
	names   map[string]string
	parents map[string]string
}

func newFakeCloud() *fakeCloud {
	return &fakeCloud{files: map[string][]byte{}, names: map[string]string{}, parents: map[string]string{}}
}
func (f *fakeCloud) ready() bool { return true }
func (f *fakeCloud) id() string  { f.n++; return fmt.Sprintf("id%d", f.n) }
func (f *fakeCloud) mkdir(_ context.Context, name, parent string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.id()
	f.names[id], f.parents[id] = name, parent
	return id, nil
}
func (f *fakeCloud) put(_ context.Context, parent, name string, data []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.id()
	f.files[id], f.names[id], f.parents[id] = data, name, parent
	return id, nil
}
func (f *fakeCloud) get(_ context.Context, id string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.files[id]
	if !ok {
		return nil, fmt.Errorf("Google Диск ответил 404")
	}
	return b, nil
}
func (f *fakeCloud) move(_ context.Context, id, name, from, to string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.names[id], f.parents[id] = name, to
	return nil
}
func (f *fakeCloud) del(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.files, id)
	return nil
}

func TestTrainOffloadToCloud(t *testing.T) {
	dir := t.TempDir()
	st := &Store{s: defaultSettings(), path: filepath.Join(dir, "settings.json")}
	st.s.TrainMode, st.s.TrainLimitMB = true, 1
	cloud := newFakeCloud()
	tr := &Trainer{dataDir: dir, store: st, cloud: cloud}
	pcm := make([]int16, 16000*2) // 2 с ≈ 64 КБ
	for i := 0; i < 20; i++ {     // ~1,25 МБ — больше лимита
		tr.Save("near", pcm, fmt.Sprintf("n%02d", i))
		time.Sleep(15 * time.Millisecond) // разные имена и время
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		off, _ := tr.remoteState()
		if !off && len(tr.remoteClips()) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("записи так и не ушли на Диск")
		}
		time.Sleep(20 * time.Millisecond)
	}
	local, remote := tr.localClips(), tr.remoteClips()
	if len(remote) != offloadBatch {
		t.Fatalf("на Диске %d записей, ждали пачку %d", len(remote), offloadBatch)
	}
	if len(local)+len(remote) != 20 {
		t.Fatalf("потерялись записи: на колонке %d, на Диске %d", len(local), len(remote))
	}
	// на Диск ушли самые старые
	oldestLocal := local[len(local)-1].Time
	for _, r := range remote {
		if r.Time.After(oldestLocal) {
			t.Fatalf("на Диск ушла не самая старая запись: %s", r.Name)
		}
	}
	if len(tr.clips()) != 20 {
		t.Fatalf("clips() = %d", len(tr.clips()))
	}
	// индекс переживает перезапуск
	tr2 := &Trainer{dataDir: dir, store: st, cloud: cloud}
	if len(tr2.remoteClips()) != offloadBatch {
		t.Fatal("индекс Диска не сохранился")
	}
	// прослушать, переразметить и удалить запись на Диске
	ctx := context.Background()
	c, ok := tr.findRemote(remote[0].Kind, remote[0].Name)
	if !ok {
		t.Fatal("findRemote")
	}
	if b, err := tr.remoteData(ctx, c); err != nil || len(b) != 44+2*len(pcm) {
		t.Fatalf("remoteData: %d байт, %v", len(b), err)
	}
	if err := tr.moveRemote(ctx, c, "pos", true); err != nil {
		t.Fatal(err)
	}
	moved, ok := tr.findRemote("pos", c.Name[:len(c.Name)-4]+"_r.wav")
	if !ok || cloud.names[moved.ID] != moved.Name {
		t.Fatalf("переразметка: %+v", moved)
	}
	if err := tr.moveRemote(ctx, moved, "delete", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := tr.findRemote("pos", moved.Name); ok || len(tr.remoteClips()) != offloadBatch-1 {
		t.Fatal("удаление с Диска")
	}
}

func TestTrainNoCloudKeepsLimit(t *testing.T) {
	dir := t.TempDir()
	st := &Store{s: defaultSettings(), path: filepath.Join(dir, "settings.json")}
	st.s.TrainMode, st.s.TrainLimitMB = true, 1
	tr := &Trainer{dataDir: dir, store: st}
	pcm := make([]int16, 16000*2)
	for i := 0; i < 20; i++ {
		tr.Save("near", pcm, fmt.Sprintf("n%02d", i))
	}
	var used int64
	for _, c := range tr.localClips() {
		used += c.Size
	}
	if used > 1<<20 {
		t.Fatalf("без Диска лимит превышен: %d", used)
	}
	if _, err := os.Stat(tr.indexPath()); err == nil {
		t.Fatal("без Диска индекс не нужен")
	}
}

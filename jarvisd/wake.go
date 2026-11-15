package main

// Слово активации «Джарвис» прямо на колонке: маленькая нейросеть (лог-мел спектр → свёрточная сеть),
// обученная на синтетике ElevenLabs и записях хозяина. Веса — файл wake.bin в папке настроек.
// Пока модели нет, Джарвиса можно позвать кнопкой «Поговорить» в веб-настройках.

import (
	"log"
	"os"
	"path/filepath"
)

type noWake struct{}

func (noWake) Feed([]int16) bool { return false }
func (noWake) Reset()            {}
func (noWake) Ready() bool       { return false }

func LoadWake(dataDir string, store *Store) WakeDetector {
	_, disabled := os.Stat(filepath.Join(dataDir, "disable-oww"))
	if os.IsNotExist(disabled) {
		// новая модель на признаках openWakeWord, если она положена рядом (oww.bin + wake_oww.bin)
		if feat, err := os.ReadFile(filepath.Join(dataDir, "oww.bin")); err == nil {
			if head, err := os.ReadFile(filepath.Join(dataDir, "wake_oww.bin")); err == nil {
				m, err := LoadOWW(feat, head, func() int { return store.Flags().WakeSensitivity })
				if err == nil {
					log.Printf("слово активации: модель openWakeWord загружена (%d КБ)", (len(feat)+len(head))/1024)
					return m
				}
				log.Printf("слово активации: openWakeWord: %v — беру прежнюю модель", err)
			}
		}
	}
	path := filepath.Join(dataDir, "wake.bin")
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("слово активации: модели %s нет — только кнопка в настройках", path)
		return noWake{}
	}
	m, err := LoadKWS(data, func() int { return store.Flags().WakeSensitivity })
	if err != nil {
		log.Printf("слово активации: %v", err)
		return noWake{}
	}
	log.Printf("слово активации: модель загружена (%d КБ)", len(data)/1024)
	return m
}

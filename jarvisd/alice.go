package main

// Команда штатной Алисе через локальный канал Quasar (127.0.0.1:9875) — как будто сказали ей голосом.
// Нужна для режима «умный дом через Алису»: короткие команды она выполняет очень быстро.
// Кадр: длина uint32 LE + protobuf; Remoting{7:"AliceCapability", 8:AliceCapabilityMethod}, 253=210.
// AliceCapabilityMethod{1: START_REQUEST(0), 2: VinsRequest{1:id, 2:event_json, 4:is_silent, 18:TEXT_INPUT(1)}}.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

func pbVarint(v uint64) []byte {
	var b []byte
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func pbInt(field int, v uint64) []byte {
	return append(pbVarint(uint64(field)<<3), pbVarint(v)...)
}

func pbBytes(field int, v []byte) []byte {
	b := append(pbVarint(uint64(field)<<3|2), pbVarint(uint64(len(v)))...)
	return append(b, v...)
}

func aliceFrame(method []byte) []byte {
	remote := append(pbBytes(7, []byte("AliceCapability")), pbBytes(8, method)...)
	env := append(pbBytes(252, remote), pbInt(253, 210)...)
	out := make([]byte, 4, 4+len(env))
	binary.LittleEndian.PutUint32(out, uint32(len(env)))
	return append(out, env...)
}

// AliceText — отправить Алисе текстовый запрос. silent=false — она ответит голосом, как обычно.
func AliceText(text string, silent bool) error {
	ev, _ := json.Marshal(map[string]any{"type": "text_input", "text": text})
	vins := pbBytes(1, []byte(fmt.Sprintf("jarvis-%d", time.Now().UnixNano())))
	vins = append(vins, pbBytes(2, ev)...)
	s := uint64(0)
	if silent {
		s = 1
	}
	vins = append(vins, pbInt(4, s)...)
	vins = append(vins, pbInt(18, 1)...)               // TEXT_INPUT
	method := append(pbInt(1, 0), pbBytes(2, vins)...) // START_REQUEST
	c, err := net.DialTimeout("tcp", "127.0.0.1:9875", time.Second)
	if err != nil {
		return fmt.Errorf("Алиса недоступна: %w", err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	_, err = c.Write(aliceFrame(method))
	time.Sleep(150 * time.Millisecond) // дать aliced прочитать кадр до закрытия
	return err
}

package main

// Keep the beginning of an utterance while the connection opens. A bounded FIFO
// prevents startup preroll from being discarded or sent as an oversized burst.
func queueMic(pending, pcm []int16) []int16 {
	const capacity = 20 * MicRate
	n := min(len(pcm), max(0, capacity-len(pending)))
	return append(pending, pcm[:n]...)
}

func takeMic(pending *[]int16, limit int) []int16 {
	n := min(len(*pending), limit)
	out := (*pending)[:n]
	*pending = (*pending)[n:]
	if len(*pending) == 0 {
		*pending = nil
	}
	return out
}

// quietPCM — в кадре нет речи (только фон): такие кадры из отставшей очереди можно пропустить.
// Микрофон колонки тихий: фон — пики 5–60, речь с пары метров — 150–700 (порог 500 съедал почти всю речь).
func quietPCM(pcm []int16) bool {
	for _, v := range pcm {
		if v > 140 || v < -140 {
			return false
		}
	}
	return true
}

// quietRun — в очереди есть ещё звук после n тихих кадров подряд с начала (пауза, которую можно пропустить).
func quietRun(pending []int16, frame, n int) bool {
	if len(pending) < (n+1)*frame {
		return false
	}
	for i := 0; i < n; i++ {
		if !quietPCM(pending[i*frame : (i+1)*frame]) {
			return false
		}
	}
	return true
}

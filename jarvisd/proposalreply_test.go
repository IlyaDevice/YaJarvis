package main

import "testing"

func TestProposalReplyLive(t *testing.T) {
	for text, want := range map[string]string{
		"да":               "accept",
		"Погнали!":         "accept",
		"ну давай":         "accept",
		"Джарвис, включай": "accept",
		"ок, ставь":        "accept",
		"не против":        "accept",
		"почему бы и нет":  "accept",
		"норм, давай":      "accept",
		"мне не нравится":  "reject",
		"нет, хуйня":       "reject",
		"Нет хуйня.":       "reject",
		"дальше":           "reject",
		"давай другое":     "reject",
		"не, не то":        "reject",
		"скучно":           "reject",
		"да ну нафиг":      "reject",
		"не интересно":     "reject",
		"следующий":        "reject",
		"неа":              "reject",
		"отмена":           "cancel",
		"ничего не надо":   "cancel",
		"передумал":        "cancel",
		"не сейчас":        "cancel",
		"я не говорил да":  "",
		"погода завтра":    "",
		"какая сейчас погода в минске и что надеть": "",
	} {
		if got := proposalReply(text); got != want {
			t.Errorf("%q → %q, ждали %q", text, got, want)
		}
	}
}

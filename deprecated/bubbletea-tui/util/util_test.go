package util

import (
	"fmt"
	"testing"
)

func TestUtil_CmdHandler(t *testing.T) {
	cmd := CmdHandler(InfoMsg{Type: InfoTypeInfo, Msg: "test"})
	msg := cmd()
	info, ok := msg.(InfoMsg)
	if !ok || info.Msg != "test" {
		t.Error("CmdHandler returned wrong message")
	}
}

func TestUtil_InfoTypes(t *testing.T) {
	e := ReportError(fmt.Errorf("err"))()
	w := ReportWarn("warn")()
	i := ReportInfo("info")()
	if e.(InfoMsg).Type != InfoTypeError {
		t.Error("error type")
	}
	if w.(InfoMsg).Type != InfoTypeWarn {
		t.Error("warn type")
	}
	if i.(InfoMsg).Type != InfoTypeInfo {
		t.Error("info type")
	}
}

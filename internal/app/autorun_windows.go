//go:build windows

package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
)

// Start with Windows is a Task Scheduler logon task, because a program that
// needs administrator rights is silently skipped by the Run registry key.
// The task runs it elevated without a UAC prompt, on battery too, with no
// time limit (schtasks' defaults would stop it after three days).
const taskName = "WarpGo"

func hidden(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return cmd
}

func autorunEnabled() bool {
	return hidden(exec.Command("schtasks", "/Query", "/TN", taskName)).Run() == nil
}

func setAutorun(on bool) error {
	if !on {
		out, err := hidden(exec.Command("schtasks", "/Delete", "/F", "/TN", taskName)).CombinedOutput()
		if err != nil && autorunEnabled() {
			return fmt.Errorf("could not remove the start-up task: %s", strings.TrimSpace(string(out)))
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	xml := `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Description>Starts WarpGo (Cloudflare WARP tunnel) when you sign in.</Description></RegistrationInfo>
  <Triggers><LogonTrigger><Enabled>true</Enabled><UserId>` + xmlEscape(currentUser()) + `</UserId><Delay>PT5S</Delay></LogonTrigger></Triggers>
  <Principals><Principal id="Author"><UserId>` + xmlEscape(currentUser()) + `</UserId><LogonType>InteractiveToken</LogonType><RunLevel>HighestAvailable</RunLevel></Principal></Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>5</Priority>
  </Settings>
  <Actions Context="Author"><Exec><Command>` + xmlEscape(exe) + `</Command><Arguments>--minimized</Arguments></Exec></Actions>
</Task>`
	path := filepath.Join(os.TempDir(), "warpgo-task.xml")
	if err := os.WriteFile(path, utf16le(xml), 0o600); err != nil {
		return err
	}
	defer os.Remove(path)
	out, err := hidden(exec.Command("schtasks", "/Create", "/F", "/TN", taskName, "/XML", path)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("could not create the start-up task: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func currentUser() string {
	d, u := os.Getenv("USERDOMAIN"), os.Getenv("USERNAME")
	if d == "" {
		return u
	}
	return d + `\` + u
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

func utf16le(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2+2*len(u))
	b[0], b[1] = 0xFF, 0xFE // BOM
	for i, c := range u {
		b[2+2*i] = byte(c)
		b[3+2*i] = byte(c >> 8)
	}
	return b
}

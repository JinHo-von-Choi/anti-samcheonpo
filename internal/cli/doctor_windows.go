//go:build windows

package cli

import (
	"net"
	"os"
	"path/filepath"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/procgroup"
)

// windowsChecks reports the Windows facts the live path depends on: the shell
// that runs checks and the unix-domain socket the hooks talk to the daemon over.
func windowsChecks() []diagnostic {
	var out []diagnostic
	if procgroup.ShellName() == "bash" {
		out = append(out, diagnostic{ID: "shell", State: "ok", Message: "Git for Windows의 bash로 검사 명령을 실행함"})
	} else {
		out = append(out, diagnostic{ID: "shell", State: "warning", Message: "Git for Windows의 bash를 찾지 못해 cmd.exe로 검사 명령을 실행함; POSIX 문법 검사(test, &&, 따옴표 규칙)는 같은 뜻으로 동작하지 않음. Git for Windows를 설치하거나 SAMCHEONPO_SHELL로 bash.exe를 지정"})
	}
	dir, err := os.MkdirTemp("", "samcheonpo-doctor-")
	if err != nil {
		return append(out, diagnostic{ID: "socket", State: "error", Message: "임시 폴더를 만들 수 없어 소켓 지원을 확인하지 못함"})
	}
	defer os.RemoveAll(dir)
	ln, err := net.Listen("unix", filepath.Join(dir, "d.sock"))
	if err != nil {
		return append(out, diagnostic{ID: "socket", State: "error", Message: "유닉스 도메인 소켓을 열 수 없음; 데몬과 훅이 통신하지 못함 (Windows 10 1803 이상 필요)"})
	}
	ln.Close()
	return append(out, diagnostic{ID: "socket", State: "ok", Message: "유닉스 도메인 소켓을 열 수 있음"})
}

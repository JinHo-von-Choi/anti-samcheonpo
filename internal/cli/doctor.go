package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/spf13/cobra"
)

type diagnostic struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Message string `json:"message"`
}
type doctorReport struct {
	Version string       `json:"version"`
	OS      string       `json:"os"`
	Arch    string       `json:"arch"`
	Checks  []diagnostic `json:"checks"`
}

func diagnose(agent, project, exe, db string, probe func(string) string, ping func() bool) doctorReport {
	r := doctorReport{Version: Version, OS: runtime.GOOS, Arch: runtime.GOARCH}
	add := func(id, state, message string) {
		r.Checks = append(r.Checks, diagnostic{ID: id, State: state, Message: message})
	}
	switch runtime.GOOS {
	case "linux", "darwin":
		add("platform", "ok", "Unix 실행 경로. 이 결과만으로 모든 배포 환경의 검증을 의미하지는 않음")
	case "windows":
		add("platform", "ok", "Windows 네이티브 실행 경로. 이 결과만으로 모든 배포 환경의 검증을 의미하지는 않음")
		for _, d := range windowsChecks() {
			add(d.ID, d.State, d.Message)
		}
	default:
		add("platform", "error", "지원하지 않는 OS; 이 환경에서는 Linux 실행 파일을 쓸 수 있는 환경(WSL 등)이 필요")
	}
	if exe != "" && hookclient.Executable(exe) != exe {
		add("hook_transport", "ok", "경량 훅 파일 발견; 현재 설치된 연결이 이 파일을 사용하는지는 설치 기록 확인 필요")
		version := hookclient.TransportVersion(hookclient.Executable(exe))
		if version == Version {
			add("hook_version", "ok", "메인 파일과 경량 훅의 버전 표기가 일치함 (무결성 인증은 아님)")
		} else {
			add("hook_version", "warning", "경량 훅 버전이 다르거나 확인할 수 없음; 같은 릴리스의 두 파일로 맞춰야 함")
		}
	} else {
		add("hook_transport", "warning", "경량 훅 파일 없음; 같은 버전의 samcheonpo-hook을 메인 파일 옆에 설치하면 기동 비용을 줄일 수 있음")
	}
	cfg, _, err := config.Load(project)
	if err != nil {
		add("config", "error", "설정을 해석할 수 없음; config.yml/.samcheonpo.yml 확인 (원문·비밀값 출력 안 함)")
	} else {
		add("config", "ok", "설정을 읽었으며 변경하지 않았음")
		if cfg.Detectors.S3.Judge.Provider == "" {
			add("judge", "ok", "의미 판정기 꺼짐; 기본 감사·감시는 API 키 없이 사용 가능")
		} else {
			add("judge", "warning", "의미 판정기 설정이 있음; 이 진단은 모델·네트워크 호출이나 인증 검사를 하지 않음")
		}
		if cfg.Privacy.ExternalJudge {
			add("external_judge", "warning", "사용자 설정에서 외부 판정을 허용함; 정책을 변경하지 않았음")
		} else {
			add("external_judge", "ok", "외부 판정 전송 비허용 정책")
		}
		if cfg.Experiment.Enabled {
			add("experiment", "warning", "실험 모드 켜짐: 일부 안내를 대조군으로 보류하고 전달하지 않음 (연구용)")
		} else {
			add("experiment", "ok", "실험 모드 꺼짐: 모든 안내가 정책대로 전달됨")
		}
		add("rollout", "ok", fmt.Sprintf("개입 단계 %s, 세션당 차단 상한 %d회", cfg.Rollout.Mode, cfg.Rollout.EscalateMaxBlocks))
	}
	version := probe(agent)
	caps, tested := adapter.Resolve(agent, version)
	if tested {
		add("agent_version", "ok", fmt.Sprintf("%s 버전이 캡처 fixture 시험 범위에 있음; 실제 세션 권한·제품 효과 인증은 아님", agent))
	} else {
		add("agent_version", "warning", fmt.Sprintf("%s 미설치·버전 미확인·시험 범위 밖 중 하나. 관찰 전용이며 감사 기능은 별도로 사용 가능", agent))
	}
	add("capabilities", "ok", capabilityText(caps))
	if ping() {
		add("daemon", "ok", "현재 홈의 데몬이 응답함")
	} else {
		add("daemon", "warning", "현재 홈의 데몬 응답 없음; 진단에서 기동하지 않았음")
	}
	if _, err := os.Stat(db); os.IsNotExist(err) {
		add("ledger", "warning", "아직 원장 없음; 진단에서 만들지 않았음")
	} else if err != nil {
		add("ledger", "error", "원장 경로 접근 실패")
	} else if version, err := ledger.Inspect(db); err != nil {
		add("ledger", "error", fmt.Sprintf("원장 무결성/스키마 확인 실패 (읽은 버전 %d); 원문·레코드는 출력하지 않음", version))
	} else {
		add("ledger", "ok", fmt.Sprintf("원장 읽기 전용 무결성 검사 통과, 스키마 %d", version))
	}
	return r
}

// capabilityText states what the connected agent lets samcheonpo do.
func capabilityText(c adapter.Caps) string {
	yes := func(b bool) string {
		if b {
			return "가능"
		}
		return "불가"
	}
	return fmt.Sprintf("실행 전 차단 %s · 실행 전 안내 %s · 결과 옆 안내 %s · 종료 보류 %s · 상태줄 %s",
		yes(c.BlockPre), yes(c.InjectPre), yes(c.InjectPost), yes(c.BlockStop), yes(c.StatusLine))
}

func doctorCmd() *cobra.Command {
	var agent, format string
	c := &cobra.Command{
		Use: "doctor", Short: "설정·원장·훅 실행 경로를 변경 없이 진단", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, ok := adapter.Profiles[agent]; !ok {
				return fmt.Errorf("알 수 없는 에이전트")
			}
			if format != "text" && format != "json" {
				return fmt.Errorf("format은 text 또는 json")
			}
			root, err := os.Getwd()
			if err != nil {
				return err
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			r := diagnose(agent, root, exe, dbPath(), adapter.ProbeVersion, func() bool {
				_, e, ok := hookclient.Query("Ping", map[string]string{}, 100*time.Millisecond)
				return ok && e == ""
			})
			failed := false
			for _, c := range r.Checks {
				failed = failed || c.State == "error"
			}
			if format == "json" {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(r); err != nil {
					return err
				}
			} else {
				for _, c := range r.Checks {
					fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s: %s\n", c.State, c.ID, c.Message)
				}
			}
			if failed {
				return fmt.Errorf("설치 진단에서 오류를 발견했다")
			}
			return nil
		},
	}
	c.Flags().StringVar(&agent, "agent", "claude", "진단할 에이전트")
	c.Flags().StringVar(&format, "format", "text", "text|json")
	return c
}

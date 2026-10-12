package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/dashboard"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/live"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/notify"
	"github.com/spf13/cobra"
)

func dashboardCmd() *cobra.Command {
	var listen, labels, codeFile, reviewQueue string
	var demo bool
	c := &cobra.Command{Use: "dashboard", Short: "요청·근거·남은 일·구간 신고를 웹 화면에서 확인", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		host, _, err := net.SplitHostPort(listen)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if !demo && (ip == nil || !ip.IsLoopback()) {
			return errors.New("실제 원장은 루프백 주소에서만 제공합니다. SSH 포워딩으로 접속할 수 있습니다")
		}
		opts := dashboard.Options{DBPath: dbPath(), LabelsDir: labels, Demo: demo, ReviewQueuePath: reviewQueue}
		if !demo {
			if codeFile == "" {
				codeFile = filepath.Join(config.Home(), fmt.Sprintf("dashboard-access-%d.code", time.Now().UnixNano()))
			}
			opts.AccessCode, err = dashboard.AccessCode(codeFile)
			if err != nil {
				return err
			}
		}
		opts.HUD = func(id string) (*notify.HUDState, error) {
			raw, err := hookclient.QueryOutput("HUD", map[string]string{"session_id": id}, time.Second)
			if err != nil {
				return nil, err
			}
			if len(raw) == 0 {
				return nil, nil
			}
			var h notify.HUDPayload
			err = json.Unmarshal(raw, &h)
			if err == nil && h.State.SessionID != id {
				return nil, errors.New("세션 식별자가 일치하지 않는 HUD")
			}
			return &h.State, err
		}
		opts.Command = func(id, name, arg string) (string, error) {
			text, problem, ok := hookclient.Query("Command", live.CommandInput{Session: id, Name: name, Arg: arg}, 5*time.Second)
			if !ok {
				return "", errors.New("실행 중인 데몬 연결 없음")
			}
			if problem != "" {
				return "", errors.New(problem)
			}
			return text, nil
		}
		handler, err := dashboard.New(opts)
		if err != nil {
			return err
		}
		ln, err := net.Listen("tcp", listen)
		if err != nil {
			return err
		}
		srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		go func() {
			<-ctx.Done()
			shutdown, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			srv.Shutdown(shutdown)
		}()
		fmt.Fprintf(cmd.OutOrStdout(), "화면: http://%s\n", ln.Addr())
		if demo {
			fmt.Fprintln(cmd.OutOrStdout(), "데모 데이터만 표시하며 실제 명령·신고는 실행하지 않습니다.")
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "접근 코드 파일: %s (0600, 화면 로그인에 사용)\n", codeFile)
		}
		err = srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}}
	c.Flags().StringVar(&listen, "listen", "127.0.0.1:18773", "화면 수신 주소; 실제 원장은 루프백만")
	c.Flags().StringVar(&labels, "labels", filepath.Join(config.Home(), "labels"), "사람 구간 라벨 저장 폴더")
	c.Flags().StringVar(&codeFile, "code-file", "", "새 접근 코드 파일; 기존 파일은 덮어쓰지 않음")
	c.Flags().BoolVar(&demo, "demo", false, "표시 예시만 제공; 실제 원장·명령·라벨에 접근하지 않음")
	c.Flags().StringVar(&reviewQueue, "review-queue", "", "고정된 사람 검토 큐 JSON; 탐지 판정은 검토 화면에서 숨김")
	return c
}

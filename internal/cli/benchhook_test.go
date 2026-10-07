package cli

import "testing"

func TestBenchHookRejectsInvalidSampleCountsBeforeStartingDaemon(t *testing.T) {
	for _, value := range []string{"0", "-1", "10001"} {
		cmd := benchHookCmd()
		cmd.SetArgs([]string{"--n", value})
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		if err := cmd.Execute(); err == nil {
			t.Fatal("invalid count accepted", value)
		}
	}
}

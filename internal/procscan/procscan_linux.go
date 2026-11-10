//go:build linux

package procscan

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// clockTicks is USER_HZ, 100 on every Linux architecture Go supports.
const clockTicks = 100

func bootTime() (time.Time, bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "btime "); ok {
			if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
				return time.Unix(n, 0), true
			}
		}
	}
	return time.Time{}, false
}

// stat reads a process's start time in clock ticks after boot.
func processIdentity(pid string) (int64, int, bool) {
	b, err := os.ReadFile("/proc/" + pid + "/stat")
	if err != nil {
		return 0, 0, false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, 0, false
	}
	f := strings.Fields(s[i+1:])
	// fields after the command: state is f[0]; starttime is field 22 of the
	// whole line, f[19] here
	if len(f) < 20 {
		return 0, 0, false
	}
	n, err := strconv.ParseInt(f[19], 10, 64)
	parent, parentErr := strconv.Atoi(f[1])
	return n, parent, err == nil && parentErr == nil
}

func startTicks(pid string) (int64, bool) { ticks, _, ok := processIdentity(pid); return ticks, ok }

func within(p, root string) bool {
	if p == "" {
		return false
	}
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

func cgroupOf(pid string) string {
	b, err := os.ReadFile("/proc/" + pid + "/cgroup")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// systemService reports a process systemd runs for a unit other than the
// one this process runs in. An agent run by a service (a CI runner) starts
// its processes in that service's own cgroup, so they stay candidates.
func systemService(pid, own string) bool {
	cg := cgroupOf(pid)
	return strings.Contains(cg, "/system.slice/") && cg != own
}

// listening maps socket inodes to the TCP ports in LISTEN state.
func listening() map[string]int {
	out := map[string]int{}
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n")[1:] {
			w := strings.Fields(l)
			if len(w) < 10 || w[3] != "0A" {
				continue
			}
			i := strings.LastIndexByte(w[1], ':')
			if port, err := strconv.ParseInt(w[1][i+1:], 16, 32); err == nil && i >= 0 {
				out[w[9]] = int(port)
			}
		}
	}
	return out
}

// Leftover lists processes left running in root: started inside one of
// the windows, by this user, not a systemd service, not excluded and not
// still a descendant of the host, which manages those itself. It stops at the
// deadline and returns what it found. A host of 0 is unknown and excludes
// nothing.
func Leftover(root string, windows []Window, exclude map[int]bool, host int, deadline time.Time) []Proc {
	if len(windows) == 0 {
		return nil
	}
	boot, ok := bootTime()
	if !ok {
		return nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	uid := os.Getuid()
	monitorExe, _ := os.Executable()
	own := cgroupOf("self")
	var out []Proc
	var ports map[string]int
	for _, e := range entries {
		if time.Now().After(deadline) {
			break
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || exclude[pid] {
			continue
		}
		dir := "/proc/" + e.Name()
		if info, err := os.Stat(dir); err != nil || ownerOf(info) != uid {
			continue
		}
		exe, _ := os.Readlink(dir + "/exe")
		if InfrastructureRole(exe, monitorExe) != "" {
			continue
		}
		cwd, _ := os.Readlink(dir + "/cwd")
		if !within(exe, root) && !within(cwd, root) {
			continue
		}
		ticks, parentPID, ok := processIdentity(e.Name())
		if !ok {
			continue
		}
		started := boot.Add(time.Duration(ticks) * time.Second / clockTicks)
		if !inWindows(started, windows) || systemService(e.Name(), own) || descendsFrom(parentPID, host) {
			continue
		}
		p := Proc{PID: pid, ParentPID: parentPID, Started: started, Exe: exe}
		if b, err := os.ReadFile(dir + "/cmdline"); err == nil {
			p.Cmd = strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", " "))
			if len(p.Cmd) > 120 {
				p.Cmd = p.Cmd[:120] + "…"
			}
		}
		if ports == nil {
			ports = listening()
		}
		if fds, err := os.ReadDir(dir + "/fd"); err == nil {
			for _, fd := range fds {
				l, err := os.Readlink(dir + "/fd/" + fd.Name())
				if inode, ok := strings.CutPrefix(l, "socket:["); err == nil && ok {
					if port, ok := ports[strings.TrimSuffix(inode, "]")]; ok {
						p.Ports = append(p.Ports, port)
					}
				}
			}
		}
		sort.Ints(p.Ports)
		// Recheck birth identity after fd/cmdline reads; a reused PID is not this candidate.
		if after, ok := startTicks(e.Name()); !ok || after != ticks {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

// maxProcessDepth bounds a walk up the process tree.
const maxProcessDepth = 64

// descendsFrom reports whether pid is host or runs under it.
func descendsFrom(pid, host int) bool {
	if host <= 1 {
		return false
	}
	for depth := 0; depth < maxProcessDepth && pid > 1; depth++ {
		if pid == host {
			return true
		}
		_, parent, ok := processIdentity(strconv.Itoa(pid))
		if !ok {
			return false
		}
		pid = parent
	}
	return false
}

// hookRunners start a hook without being the agent: the shell the host
// wraps a hook command in, and the hook itself.
var hookRunners = map[string]bool{"sh": true, "bash": true, "dash": true, "zsh": true, "fish": true, "samcheonpo-hook": true, "samcheonpo": true}

// HostPID finds the agent process a hook runs under: the first ancestor of
// the hook that is not a shell. It returns 0 when that is not known or would
// be the init system, which parents every orphan.
func HostPID(hook int) int {
	pid := hook
	for depth := 0; depth < maxProcessDepth && pid > 1; depth++ {
		_, parent, ok := processIdentity(strconv.Itoa(pid))
		if !ok || parent <= 1 {
			return 0
		}
		exe, err := os.Readlink("/proc/" + strconv.Itoa(parent) + "/exe")
		if err != nil {
			return 0
		}
		name := filepath.Base(exe)
		if name == "systemd" || name == "init" {
			return 0
		}
		if !hookRunners[name] {
			return parent
		}
		pid = parent
	}
	return 0
}

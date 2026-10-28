package classify

import (
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/pathnorm"
)

// shellWords splits one shell stage into words, honouring single and double
// quotes and backslashes. A word keeps no quotes; expansions stay as text.
func shellWords(s string) []string {
	var out []string
	var b strings.Builder
	in := false
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			} else {
				b.WriteByte(c)
			}
		case quote == '"':
			if c == '"' {
				quote = 0
			} else if c == '\\' && i+1 < len(s) {
				i++
				b.WriteByte(s[i])
			} else {
				b.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote, in = c, true
		case c == '\\' && i+1 < len(s):
			i++
			b.WriteByte(s[i])
			in = true
		case c == ' ' || c == '\t' || c == '\n':
			if in {
				out = append(out, b.String())
				b.Reset()
				in = false
			}
		default:
			b.WriteByte(c)
			in = true
		}
	}
	if in {
		out = append(out, b.String())
	}
	return out
}

// stripWrappers drops the words that only run the real program: sudo with
// its options, env with assignments, nice, time, exec.
func stripWrappers(w []string) []string {
	for len(w) > 0 {
		switch base := pathnorm.CommandBase(w[0]); {
		case base == "sudo":
			w = w[1:]
			for len(w) > 0 && strings.HasPrefix(w[0], "-") {
				opt := w[0]
				w = w[1:]
				if (opt == "-u" || opt == "-g") && len(w) > 0 {
					w = w[1:]
				}
			}
		case base == "env" || base == "nice" || base == "time" || base == "exec" || base == "command":
			w = w[1:]
			for len(w) > 0 && (strings.HasPrefix(w[0], "-") || strings.Contains(w[0], "=")) {
				w = w[1:]
			}
		case strings.Contains(w[0], "=") && !strings.HasPrefix(w[0], "="):
			w = w[1:] // VAR=value prefix
		default:
			return w
		}
	}
	return w
}

// cmdStage is one shell stage of a command, with the here-document bodies
// its program reads.
type cmdStage struct {
	words []string
	body  string // here-document body read by this stage's program
}

// stagesOf splits a command into stages, wrappers removed; sh -c and
// bash -c contribute the stages of their argument.
func stagesOf(cmd string) []cmdStage {
	outer, docs := splitHeredocs(cmd)
	var out []cmdStage
	for _, st := range fp.SplitStages(outer) {
		w := stripWrappers(shellWords(st))
		if len(w) == 0 {
			continue
		}
		s := cmdStage{words: w}
		for _, d := range docs {
			if pathnorm.CommandBase(d.reader) == pathnorm.CommandBase(w[0]) {
				s.body += d.body + "\n"
			}
		}
		// sh -c / bash -c run their argument as a command of their own
		if b := pathnorm.CommandBase(w[0]); (b == "sh" || b == "bash" || b == "zsh") && len(w) >= 3 && w[1] == "-c" {
			out = append(out, stagesOf(w[2])...)
			continue
		}
		out = append(out, s)
	}
	return out
}

var dryRunRe = regexp.MustCompile(`^--?(?:dry-run|dryrun|check|plan|diff|what-if)(?:=.*)?$`)

// Deploy reports whether a command deploys: a deploy script, make deploy,
// kubectl apply/set image/rollout restart, helm upgrade/install, terraform
// apply, fly deploy, vercel --prod, a command the contract names, or a
// systemctl start/stop/restart of a unit the contract names as production.
// A dry run, plan, diff or template of the same tools is not a deploy, and
// other service control (a database restarted for tests) stays what it was.
func Deploy(cmd string, commands, services []string) bool {
	norm, _ := fp.NormalizeCmd(cmd)
	for _, c := range commands {
		if c = strings.TrimSpace(c); c != "" {
			if n, _ := fp.NormalizeCmd(c); n != "" && (norm == n || strings.HasPrefix(norm, n+" ")) {
				return true
			}
		}
	}
	for _, st := range stagesOf(cmd) {
		if deployStage(st.words, services) {
			return true
		}
	}
	return false
}

func deployStage(w []string, services []string) bool {
	for _, x := range w[1:] {
		if dryRunRe.MatchString(x) {
			return false
		}
	}
	prog := pathnorm.CommandBase(w[0])
	arg := func(i int) string {
		if i < len(w) {
			return w[i]
		}
		return ""
	}
	script := func(p string) bool {
		b := strings.ToLower(filepath.Base(p))
		return strings.HasPrefix(b, "deploy") && (strings.HasSuffix(b, ".sh") || !strings.Contains(b, "."))
	}
	switch prog {
	case "bash", "sh", "zsh":
		return script(arg(1))
	case "make", "just", "task":
		return strings.HasPrefix(arg(1), "deploy")
	case "kubectl":
		return arg(1) == "apply" || arg(1) == "set" && arg(2) == "image" || arg(1) == "rollout" && arg(2) == "restart"
	case "helm":
		return arg(1) == "upgrade" || arg(1) == "install"
	case "terraform", "tofu":
		return arg(1) == "apply"
	case "fly", "flyctl":
		return arg(1) == "deploy"
	case "vercel":
		for _, x := range w[1:] {
			if x == "--prod" {
				return true
			}
		}
	case "systemctl":
		if a := arg(1); a == "start" || a == "stop" || a == "restart" || a == "reload" {
			for _, u := range w[2:] {
				for _, s := range services {
					if u == s || u == strings.TrimSuffix(s, ".service") || u+".service" == s {
						return true
					}
				}
			}
		}
	}
	return script(w[0])
}

// DataWriteInfo is what a command does to a database.
type DataWriteInfo struct {
	Tool string // the client or migration tool
	// Write is a statement or command that changes data or schema.
	Write bool
	// MigrationHistory is a hand-written change to a migration history
	// table (_sqlx_migrations, schema_migrations, ...).
	MigrationHistory bool
	// MigrationTool is a migration tool rolling back or resetting.
	MigrationTool bool
	// Hosts are database hosts the command names; Explicit is true when it
	// names its target (a host or a connection string) instead of reading
	// one from the environment.
	Hosts    []string
	Explicit bool
	// EnvFiles are the files the same command loads into its environment.
	EnvFiles []string
	// Local is true when every named host is this machine.
	Local bool
}

var (
	sqlStringRe  = regexp.MustCompile(`'(?:[^']|'')*'`)
	sqlCommentRe = regexp.MustCompile(`(?s)--[^\n]*|/\*.*?\*/`)
	sqlWriteRe   = regexp.MustCompile(`(?i)\b(?:INSERT|UPDATE|DELETE|ALTER|DROP|TRUNCATE|CREATE|GRANT|REVOKE|MERGE|UPSERT|REPLACE\s+INTO)\b`)
	sqlHistoryRe = regexp.MustCompile(`(?i)\b(?:INSERT\s+INTO|UPDATE|DELETE\s+FROM|TRUNCATE(?:\s+TABLE)?)\s+(?:ONLY\s+)?(?:"?[\w]+"?\.)?"?(?:_sqlx_migrations|schema_migrations|flyway_schema_history|alembic_version|__diesel_schema_migrations|knex_migrations)\b`)
	redisWriteRe = regexp.MustCompile(`(?i)^(?:flushall|flushdb|del|unlink|set|setex|mset|hset|hdel|lpush|rpush|sadd|srem|zadd|zrem|expire|rename|config)$`)
	mongoWriteRe = regexp.MustCompile(`\.(?:insert\w*|update\w*|delete\w*|drop\w*|replace\w*|bulkWrite|remove|findOneAnd\w+|create\w*)\s*\(`)
	urlRe        = regexp.MustCompile(`^(?:postgres(?:ql)?|mysql|mariadb|mongodb(?:\+srv)?|redis|rediss)://`)
)

// sqlWrites reads a SQL text with its literals and comments removed.
func sqlWrites(sql string) (write, history bool) {
	clean := sqlStringRe.ReplaceAllString(sqlCommentRe.ReplaceAllString(sql, " "), "''")
	return sqlWriteRe.MatchString(clean), sqlHistoryRe.MatchString(clean)
}

func localHost(h string) bool {
	switch strings.ToLower(h) {
	case "localhost", "127.0.0.1", "::1", "[::1]", "":
		return true
	}
	return strings.HasPrefix(h, "/") // a unix socket folder
}

// DataWrite reads the database writes of a shell command. It never reads
// files: a statement in a file named with -f is not seen.
func DataWrite(cmd string) DataWriteInfo {
	var info DataWriteInfo
	for _, st := range stagesOf(cmd) {
		w := st.words
		prog := pathnorm.CommandBase(w[0])
		switch prog {
		case "source", ".":
			if len(w) > 1 {
				info.EnvFiles = append(info.EnvFiles, w[1])
			}
			continue
		}
		for i, x := range w {
			if x == "--env-file" && i+1 < len(w) {
				info.EnvFiles = append(info.EnvFiles, w[i+1])
			} else if strings.HasPrefix(x, "--env-file=") {
				info.EnvFiles = append(info.EnvFiles, strings.TrimPrefix(x, "--env-file="))
			}
		}
		var sql []string
		host := func(h string) {
			info.Hosts = append(info.Hosts, h)
			info.Explicit = true
		}
		target := func(x string) {
			if urlRe.MatchString(x) {
				if u, err := url.Parse(x); err == nil {
					host(u.Hostname())
				} else {
					info.Explicit = true
				}
			}
		}
		switch prog {
		case "psql", "mysql", "mariadb", "sqlite3", "pgcli", "mycli":
			info.Tool = prog
			for i := 1; i < len(w); i++ {
				x := w[i]
				next := ""
				if i+1 < len(w) {
					next = w[i+1]
				}
				switch {
				case x == "-c" || x == "-e" || x == "--command" || x == "--execute":
					sql = append(sql, next)
					i++
				case strings.HasPrefix(x, "--command=") || strings.HasPrefix(x, "--execute="):
					sql = append(sql, x[strings.IndexByte(x, '=')+1:])
				case x == "-h" || x == "--host":
					host(next)
					i++
				case strings.HasPrefix(x, "--host="):
					host(strings.TrimPrefix(x, "--host="))
				case strings.HasPrefix(x, "-h") && len(x) > 2:
					host(x[2:])
				default:
					target(x)
				}
				if prog == "sqlite3" && i == 1 && !strings.HasPrefix(x, "-") {
					info.Explicit = true // the database file is the target
					info.Hosts = append(info.Hosts, "")
				}
			}
			if prog == "sqlite3" && len(w) > 2 && !strings.HasPrefix(w[len(w)-1], "-") {
				sql = append(sql, w[len(w)-1])
			}
			sql = append(sql, st.body)
		case "redis-cli":
			info.Tool = prog
			for i := 1; i < len(w); i++ {
				x := w[i]
				switch {
				case x == "-h" && i+1 < len(w):
					host(w[i+1])
					i++
				case x == "-u" && i+1 < len(w):
					target(w[i+1])
					i++
				case x == "-p" || x == "-n" || x == "-a" || x == "--user" || x == "--pass":
					i++
				case strings.HasPrefix(x, "-"):
				default:
					if redisWriteRe.MatchString(x) {
						info.Write = true
					}
					i = len(w) // the first word after the options is the command
				}
			}
			for _, l := range strings.Split(st.body, "\n") {
				if f := strings.Fields(l); len(f) > 0 && redisWriteRe.MatchString(f[0]) {
					info.Write = true
				}
			}
		case "mongosh", "mongo":
			info.Tool = prog
			code := st.body
			for i := 1; i < len(w); i++ {
				switch x := w[i]; {
				case x == "--eval" && i+1 < len(w):
					code += "\n" + w[i+1]
					i++
				case x == "--host" && i+1 < len(w):
					host(w[i+1])
					i++
				default:
					target(x)
				}
			}
			if mongoWriteRe.MatchString(code) {
				info.Write = true
			}
		case "sqlx":
			if len(w) >= 3 && w[1] == "migrate" && w[2] == "revert" {
				info.Tool, info.MigrationTool, info.Write = prog, true, true
			}
		case "diesel":
			if len(w) >= 3 && w[1] == "migration" && (w[2] == "revert" || w[2] == "redo") {
				info.Tool, info.MigrationTool, info.Write = prog, true, true
			}
		case "alembic":
			if len(w) >= 2 && w[1] == "downgrade" {
				info.Tool, info.MigrationTool, info.Write = prog, true, true
			}
		case "prisma":
			if len(w) >= 3 && w[1] == "migrate" && w[2] == "reset" {
				info.Tool, info.MigrationTool, info.Write = prog, true, true
			}
		case "npx", "bunx", "pnpx":
			if len(w) >= 4 && pathnorm.CommandBase(w[1]) == "prisma" && w[2] == "migrate" && w[3] == "reset" {
				info.Tool, info.MigrationTool, info.Write = "prisma", true, true
			}
		}
		for _, q := range sql {
			wr, hist := sqlWrites(q)
			info.Write = info.Write || wr
			info.MigrationHistory = info.MigrationHistory || hist
		}
	}
	info.Local = len(info.Hosts) > 0
	for _, h := range info.Hosts {
		if !localHost(h) {
			info.Local = false
		}
	}
	return info
}

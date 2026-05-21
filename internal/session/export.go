package session

import (
	"bytes"
	"html/template"
	"time"
)

var parsedExportTemplate = template.Must(template.New("session-export").Funcs(template.FuncMap{
	"time": formatExportTime,
	"fold": shouldFoldMessage,
}).Parse(exportTemplate))

func ExportHTML(detail *SessionDetail) ([]byte, error) {
	var out bytes.Buffer
	if err := parsedExportTemplate.Execute(&out, detail); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func formatExportTime(unix int64) string {
	if unix == 0 {
		return ""
	}
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}

func shouldFoldMessage(role string) bool {
	return role == "system" || role == "tool"
}

const exportTemplate = `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="utf-8">
<title>{{.Session.Title}}</title>
<style>
:root{--bg:#f7fbff;--surface:#ffffff;--border:#dbeafe;--text:#102033;--text-muted:#64748b;--accent:#2563eb;--user-bg:#dcfce7;--user-border:#86efac;--user-text:#14532d;--user-label:#166534;--technical-border:#f59e0b;--shadow:0 1px 3px rgba(0,0,0,.06)}
@media(prefers-color-scheme:dark){:root{--bg:#070b14;--surface:rgba(15,23,42,.94);--border:#263449;--text:#e5edf7;--text-muted:#94a3b8;--accent:#38bdf8;--user-bg:#052e1b;--user-border:#22c55e;--user-text:#bbf7d0;--user-label:#86efac;--technical-border:#f59e0b;--shadow:0 1px 3px rgba(0,0,0,.2)}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--text);font-family:'Outfit',Inter,system-ui,-apple-system,BlinkMacSystemFont,sans-serif;line-height:1.6}
header{padding:28px 36px;border-bottom:1px solid var(--border);background:var(--surface)}
h1{margin:0 0 10px;font-size:24px;font-weight:700}
.meta{color:var(--text-muted);font-size:13px}
main{max-width:980px;margin:0 auto;padding:28px}
.message{border:1px solid var(--border);border-radius:12px;padding:16px;margin:0 0 12px;background:var(--surface);box-shadow:var(--shadow)}
.message.user{background:var(--user-bg);border:1px solid var(--user-border);color:var(--user-text)}
.message.user .role{color:var(--user-label)}
.message.assistant{border-left:4px solid var(--accent)}
.message.system,.message.tool{border-left:4px solid var(--technical-border)}
.role{font-weight:700;text-transform:uppercase;font-size:11px;letter-spacing:.06em;color:var(--text-muted);margin-bottom:8px}
pre{white-space:pre-wrap;word-break:break-word;font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace;font-size:13px;margin:0}
summary{cursor:pointer;font-weight:700;color:var(--text-muted);font-size:12px;letter-spacing:.04em;text-transform:uppercase}
@media print{body{background:#fff;color:#111827}.message,header{background:#fff;border-color:#d1d5db;box-shadow:none}.meta{color:#4b5563}}
</style>
</head>
<body>
<header>
<h1>{{.Session.Title}}</h1>
<div class="meta">Project: {{.Session.ProjectPath}}</div>
<div class="meta">Created: {{.Session.CreatedAt.Format "2006-01-02 15:04:05 MST"}} · Last active: {{.Session.LastActiveAt.Format "2006-01-02 15:04:05 MST"}}</div>
</header>
<main>
{{range .Messages}}
<section class="message {{.Role}}">
{{if fold .Role}}
<details>
<summary>{{.Role}}{{if .Timestamp}} · {{time .Timestamp}}{{end}}</summary>
<pre>{{.Content}}</pre>
</details>
{{else}}
<div class="role">{{.Role}}{{if .Timestamp}} · {{time .Timestamp}}{{end}}</div>
<pre>{{.Content}}</pre>
{{end}}
</section>
{{end}}
</main>
</body>
</html>
`

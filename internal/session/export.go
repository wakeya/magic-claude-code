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
body{margin:0;background:#111827;color:#e5e7eb;font-family:Inter,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;line-height:1.55}
header{padding:28px 36px;border-bottom:1px solid #374151;background:#0f172a}
h1{margin:0 0 10px;font-size:26px}
.meta{color:#9ca3af;font-size:13px}
main{max-width:980px;margin:0 auto;padding:28px}
.message{border:1px solid #374151;border-radius:8px;padding:16px;margin:0 0 16px;background:#1f2937}
.message.user{border-left:4px solid #38bdf8}
.message.assistant{border-left:4px solid #a78bfa}
.message.system,.message.tool{border-left:4px solid #f59e0b}
.role{font-weight:700;text-transform:uppercase;font-size:12px;color:#cbd5e1;margin-bottom:8px}
pre{white-space:pre-wrap;word-break:break-word;font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,"Liberation Mono",monospace;font-size:13px}
summary{cursor:pointer;font-weight:700}
@media print{body{background:#fff;color:#111827}.message,header{background:#fff;border-color:#d1d5db}.meta{color:#4b5563}}
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

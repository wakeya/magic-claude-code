package session

import (
	"bytes"
	"html/template"
	"strings"
	"time"
)

// OutlineItem represents a summary item for the session outline
type OutlineItem struct {
	Index     int
	Preview   string
	Timestamp int64
}

var parsedExportTemplate = template.Must(template.New("session-export").Funcs(template.FuncMap{
	"time":         formatExportTime,
	"fold":         shouldFoldMessage,
	"outlineTime": func(ts int64) string {
		if ts == 0 {
			return ""
		}
		return time.Unix(ts, 0).Format("2006-01-02 15:04:05")
	},
}).Parse(exportTemplate))

func ExportHTML(detail *SessionDetail, theme, locale string) ([]byte, error) {
	var out bytes.Buffer
	data := map[string]any{
		"Session":       detail.Session,
		"Messages":      detail.Messages,
		"Theme":         theme,
		"Locale":        locale,
		"OutlineItems":  buildOutlineItems(detail.Messages),
		"OutlineCount":  len(buildOutlineItems(detail.Messages)),
	}
	if err := parsedExportTemplate.Execute(&out, data); err != nil {
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

func buildOutlineItems(messages []Message) []OutlineItem {
	var items []OutlineItem
	for i, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		preview := previewText(msg.Content)
		items = append(items, OutlineItem{
			Index:     i,
			Preview:   preview,
			Timestamp: msg.Timestamp,
		})
	}
	return items
}

func previewText(content string) string {
	compact := strings.Join(strings.Fields(content), " ")
	if len(compact) <= 50 {
		return compact
	}
	return compact[:50] + "..."
}

const exportTemplate = `<!DOCTYPE html>
<html lang="zh" data-theme="{{.Theme}}">
<head>
<meta charset="utf-8">
<title>{{.Session.Title}}</title>
<style>
:root,:root[data-theme="light"]{--bg:#f7fbff;--surface:#ffffff;--border:#dbeafe;--text:#102033;--text-muted:#64748b;--accent:#2563eb;--user-bg:#dcfce7;--user-border:#86efac;--user-text:#14532d;--user-label:#166534;--technical-border:#f59e0b;--shadow:0 1px 3px rgba(0,0,0,.06);--outline-bg:#f1f5f9;--outline-hover:#e2e8f0;--outline-active-bg:#dbeafe;--outline-active-border:#2563eb}
:root[data-theme="dark"]{--bg:#070b14;--surface:rgba(15,23,42,.94);--border:#263449;--text:#e5edf7;--text-muted:#94a3b8;--accent:#38bdf8;--user-bg:#052e1b;--user-border:#22c55e;--user-text:#bbf7d0;--user-label:#86efac;--technical-border:#f59e0b;--shadow:0 1px 3px rgba(0,0,0,.2);--outline-bg:rgba(15,23,42,.6);--outline-hover:#1e293b;--outline-active-bg:rgba(37,99,235,.15);--outline-active-border:#38bdf8}
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

/* Layout */
.layout{display:flex;max-width:1280px;margin:0 auto;padding:0 16px;gap:24px;align-items:flex-start}
main{flex:1;min-width:0}

/* Outline panel */
.outline-panel{width:240px;flex-shrink:0;position:sticky;top:16px;max-height:calc(100vh - 32px);overflow-y:auto;overscroll-behavior:contain;background:var(--outline-bg);border:1px solid var(--border);border-radius:12px;padding:12px;display:flex;flex-direction:column;gap:8px}
.outline-title{font-size:11px;font-weight:700;text-transform:uppercase;letter-spacing:.08em;color:var(--text-muted);padding:0 4px 4px}
.outline-items{display:flex;flex-direction:column;gap:4px}
.outline-item{background:transparent;border:1px solid transparent;border-radius:8px;padding:8px;cursor:pointer;text-align:left;transition:background .15s,border-color .15s;width:100%}
.outline-item:hover{background:var(--outline-hover)}
.outline-item.outline-active{background:var(--outline-active-bg);border-color:var(--outline-active-border)}
.outline-item-preview{font-size:13px;line-height:1.4;color:var(--text);display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden}
.outline-item-time{font-size:11px;color:var(--text-muted);margin-top:4px}
.back-to-top{display:none;position:fixed;bottom:20px;right:20px;width:40px;height:40px;border-radius:50%;border:1px solid var(--border);background:var(--surface);color:var(--text-muted);cursor:pointer;transition:background .15s,color .15s;box-shadow:0 2px 8px rgba(0,0,0,.15);font-size:18px;z-index:40}
.back-to-top:hover{background:var(--outline-hover);color:var(--text)}
@media(max-width:1023px){.back-to-top{display:flex;align-items:center;justify-content:center}}

/* Small screen: hide fixed panel, show toggle */
.outline-toggle{display:none;position:fixed;bottom:20px;right:20px;width:48px;height:48px;border-radius:50%;border:none;background:var(--accent);color:#fff;font-size:20px;cursor:pointer;box-shadow:0 2px 8px rgba(0,0,0,.2);z-index:50;align-items:center;justify-content:center}
.outline-modal{border:none;border-radius:16px;padding:0;max-width:320px;width:90vw;background:var(--surface);box-shadow:0 8px 32px rgba(0,0,0,.2);color:var(--text)}
.outline-modal::backdrop{background:rgba(0,0,0,.4)}
.outline-modal-header{display:flex;align-items:center;justify-content:space-between;padding:16px;border-bottom:1px solid var(--border)}
.outline-modal-title{font-size:14px;font-weight:700;color:var(--text)}
.outline-modal-close{border:none;background:transparent;color:var(--text-muted);cursor:pointer;font-size:20px;padding:4px;line-height:1}
.outline-modal-body{padding:12px;max-height:60vh;overflow-y:auto}
.outline-modal-backtop{display:flex;justify-content:flex-end;padding:12px;border-top:1px solid var(--border)}

@media(max-width:1023px){
.outline-panel{display:none}
.outline-toggle{display:flex}
.layout{padding:0 8px}
}
</style>
</head>
<body>
<header>
<h1>{{.Session.Title}}</h1>
<div class="meta">Project: {{.Session.ProjectPath}}</div>
<div class="meta">Created: {{.Session.CreatedAt.Format "2006-01-02 15:04:05 MST"}} · Last active: {{.Session.LastActiveAt.Format "2006-01-02 15:04:05 MST"}}</div>
</header>
<div class="layout">
<main>
{{range $idx, $msg := .Messages}}
<section id="msg-{{$idx}}" class="message {{$msg.Role}}">
{{if fold $msg.Role}}
<details>
<summary>{{$msg.Role}}{{if $msg.Timestamp}} · {{time $msg.Timestamp}}{{end}}</summary>
<pre>{{$msg.Content}}</pre>
</details>
{{else}}
<div class="role">{{$msg.Role}}{{if $msg.Timestamp}} · {{time $msg.Timestamp}}{{end}}</div>
<pre>{{$msg.Content}}</pre>
{{end}}
</section>
{{end}}
</main>
<aside class="outline-panel">
<div class="outline-title">{{if eq .Locale "zh"}}大纲 ({{.OutlineCount}}){{else}}Outline ({{.OutlineCount}}){{end}}</div>
<div class="outline-items">
{{range .OutlineItems}}
<button class="outline-item" onclick="jumpToMsg('msg-{{.Index}}')">
<div class="outline-item-preview">{{.Preview}}</div>
<div class="outline-item-time">{{outlineTime .Timestamp}}</div>
</button>
{{end}}
</div>
</aside>
</div>
<button class="back-to-top" onclick="backToTop()" title="{{if eq .Locale "zh"}}回到顶部{{else}}Back to top{{end}}">↑</button>
<button class="outline-toggle" onclick="document.getElementById('outline-modal').showModal()">☰</button>
<dialog class="outline-modal" id="outline-modal">
<div class="outline-modal-header">
<span class="outline-modal-title">{{if eq .Locale "zh"}}大纲 ({{.OutlineCount}}){{else}}Outline ({{.OutlineCount}}){{end}}</span>
<button class="outline-modal-close" onclick="document.getElementById('outline-modal').close()">×</button>
</div>
<div class="outline-modal-body">
<div class="outline-items">
{{range .OutlineItems}}
<button class="outline-item" onclick="jumpToMsg('msg-{{.Index}}')">
<div class="outline-item-preview">{{.Preview}}</div>
<div class="outline-item-time">{{outlineTime .Timestamp}}</div>
</button>
{{end}}
</div>
</div>
<div class="outline-modal-backtop">
<button class="back-to-top" onclick="backToTop()" title="Back to top">↑ Back to top</button>
</div>
</dialog>
<script>
(function(){
var sections=document.querySelectorAll('section[id]');
var items=document.querySelectorAll('.outline-item');
var observer=new IntersectionObserver(function(entries){
entries.forEach(function(entry){
if(entry.isIntersecting){
var id=entry.target.id;
items.forEach(function(item){item.classList.remove('outline-active')});
var btn=document.querySelector('.outline-item[onclick*="'+id+'"]');
if(btn)btn.classList.add('outline-active');
}
});
},{rootMargin:'-20% 0px -60% 0px',threshold:0});
sections.forEach(function(sec){observer.observe(sec)});

window.jumpToMsg=function(id){
var el=document.getElementById(id);
if(el){el.scrollIntoView({behavior:'smooth'})}
var modal=document.getElementById('outline-modal');
if(modal&&modal.open){modal.close()}
};

window.backToTop=function(){
window.scrollTo({top:0,behavior:'smooth'})
};
})();
</script>
</body>
</html>
`

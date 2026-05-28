# Export HTML Outline Navigation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a right-side outline navigation panel and back-to-top button to exported HTML session files, with click-to-navigate, scroll auto-highlight, and responsive modal for small screens.

**Architecture:** All changes are in `internal/session/export.go`. The Go side builds `OutlineItem` data (filtered user messages with preview and timestamp) and injects it into the template. The HTML template gains a `<div class="layout">` with `<main>` + `<aside>`, responsive CSS using existing CSS variables, and vanilla JS for IntersectionObserver-based scroll tracking.

**Tech Stack:** Go 1.26, Go `html/template`, vanilla JS (IntersectionObserver), CSS custom properties

---

## File Structure

```
internal/session/export.go      — Modify: add OutlineItem struct, helper fns, template funcs, update exportTemplate
internal/session/export_test.go  — Modify: add tests for outline functionality
```

---

## Task 1: Add OutlineItem Struct and Helper Functions

**Files:**
- Modify: `internal/session/export.go`

- [ ] **Step 1: Add `OutlineItem` struct after the imports**

Add after line 7 (`time` import), before `var parsedExportTemplate`:

```go
type OutlineItem struct {
	Index     int
	Preview   string
	Timestamp int64
}
```

- [ ] **Step 2: Add `buildOutlineItems` function after `shouldFoldMessage`**

```go
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
```

- [ ] **Step 3: Add `formatOutlineTime` template function**

Add to `template.FuncMap` in `parsedExportTemplate` initialization:

```go
"outlineTime": func(ts int64) string {
	if ts == 0 {
		return ""
	}
	return time.Unix(ts, 0).Format("2006-01-02 15:04:05")
},
```

- [ ] **Step 4: Update `ExportHTML` to pass outline data**

Modify the `data` map in `ExportHTML` to include:

```go
data := map[string]any{
	"Session":       detail.Session,
	"Messages":      detail.Messages,
	"Theme":         theme,
	"OutlineItems":  buildOutlineItems(detail.Messages),
}
```

- [ ] **Step 5: Add `strings` import**

Add `"strings"` to the import block if not already present.

- [ ] **Step 6: Run tests**

Run: `go test ./internal/session/... -v`
Expected: All existing tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/session/export.go
git commit -m "feat: add OutlineItem struct and buildOutlineItems for export outline"
```

---

## Task 2: Rewrite exportTemplate — HTML Structure and CSS

**Files:**
- Modify: `internal/session/export.go` (the `exportTemplate` constant, lines ~38-86)

The current template starts at line 38. Replace the entire `const exportTemplate` block. The new template keeps all existing styles/markup for header, main, messages, and print media, but restructures the body to use a layout wrapper, adds outline panel, toggle button, modal dialog, and associated CSS.

- [ ] **Step 1: Replace the `exportTemplate` constant**

Replace the entire `const exportTemplate = \`...\`` block (lines 38-86) with the following new template. The new template:
- Wraps content in `<div class="layout">` with `<main>` and `<aside>`
- Adds all outline-related CSS (outline-panel, outline-item, outline-active, back-to-top, outline-toggle, outline-modal)
- Adds outline toggle button and `<dialog>` for small screens
- Adds `<script>` tag for interactions
- Keeps all existing styles (CSS variables, body, header, messages, etc.)
- Keeps all existing body content (header, messages loop)

```go
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
.back-to-top{display:flex;align-items:center;justify-content:center;width:32px;height:32px;border-radius:8px;border:1px solid var(--border);background:var(--surface);color:var(--text-muted);cursor:pointer;transition:background .15s,color .15s;margin-left:auto}
.back-to-top:hover{background:var(--outline-hover);color:var(--text)}

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
<div class="outline-title">Outline</div>
<div class="outline-items">
{{range .OutlineItems}}
<button class="outline-item" onclick="jumpToMsg('msg-{{.Index}}')">
<div class="outline-item-preview">{{.Preview}}</div>
<div class="outline-item-time">{{outlineTime .Timestamp}}</div>
</button>
{{end}}
</div>
<div style="display:flex;justify-content:flex-end;padding-top:8px">
<button class="back-to-top" onclick="backToTop()" title="Back to top">↑</button>
</div>
</aside>
</div>
<button class="outline-toggle" onclick="document.getElementById('outline-modal').showModal()">☰</button>
<dialog class="outline-modal" id="outline-modal">
<div class="outline-modal-header">
<span class="outline-modal-title">Outline</span>
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
```

**Critical template details:**
- The `{{range $idx, $msg := .Messages}}` syntax captures the index as `$idx` and message as `$msg`, so `{{$idx}}` gives the position for the `id` attribute
- `{{outlineTime .Timestamp}}` calls the new template function
- `{{.Preview}}` is the pre-computed preview string from `buildOutlineItems`

- [ ] **Step 2: Verify template parses without error**

Run: `go build ./internal/session/...`
Expected: Build succeeds with no errors.

- [ ] **Step 3: Run existing tests**

Run: `go test ./internal/session/... -v`
Expected: All existing tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/session/export.go
git commit -m "feat: add outline panel HTML structure, CSS styles, and interactive JS to export template"
```

---

## Task 3: Add Tests for Outline Functionality

**Files:**
- Modify: `internal/session/export_test.go`

- [ ] **Step 1: Add test for outline items structure**

Add after the existing tests:

```go
func TestExportHTMLContainsOutline(t *testing.T) {
	html := exportTestHTML(t)
	if !strings.Contains(html, "Outline") {
		t.Fatalf("export should contain Outline panel: %s", html)
	}
}

func TestExportHTMLOutlineItems(t *testing.T) {
	html := exportTestHTML(t)
	// The test data has 1 user message ("hello"), so there should be 1 outline item
	// Count outline-item buttons
	count := strings.Count(html, "class=\"outline-item\"")
	if count != 1 {
		t.Fatalf("expected 1 outline item (1 user message), got %d: %s", count, html)
	}
}

func TestExportHTMLOutlineHasPreviewText(t *testing.T) {
	html := exportTestHTML(t)
	// The user message is "hello" (5 chars, less than 50)
	if !strings.Contains(html, "hello") {
		t.Fatalf("outline preview should contain 'hello': %s", html)
	}
}

func TestExportHTMLMessageHasAnchorID(t *testing.T) {
	html := exportTestHTML(t)
	// The user message is at index 1
	if !strings.Contains(html, `id="msg-1"`) {
		t.Fatalf("user message should have id=\"msg-1\": %s", html)
	}
}

func TestExportHTMLOutlineHasBackToTop(t *testing.T) {
	html := exportTestHTML(t)
	if !strings.Contains(html, "back-to-top") {
		t.Fatalf("export should contain back-to-top: %s", html)
	}
}

func TestExportHTMLOutlineScript(t *testing.T) {
	html := exportTestHTML(t)
	if !strings.Contains(html, "jumpToMsg") {
		t.Fatalf("export should contain jumpToMsg JS: %s", html)
	}
	if !strings.Contains(html, "IntersectionObserver") {
		t.Fatalf("export should contain IntersectionObserver: %s", html)
	}
	if !strings.Contains(html, "backToTop") {
		t.Fatalf("export should contain backToTop JS: %s", html)
	}
}

func TestExportHTMLOutlineModal(t *testing.T) {
	html := exportTestHTML(t)
	if !strings.Contains(html, "outline-modal") {
		t.Fatalf("export should contain outline-modal: %s", html)
	}
	if !strings.Contains(html, "outline-toggle") {
		t.Fatalf("export should contain outline-toggle: %s", html)
	}
}
```

- [ ] **Step 2: Run new tests**

Run: `go test ./internal/session/... -v -run TestExportHTMLOutline`
Expected: All new tests pass.

- [ ] **Step 3: Run all tests**

Run: `go test ./internal/session/... -v`
Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/session/export_test.go
git commit -m "test: add outline functionality tests"
```

---

## Task 4: Final Verification

- [ ] **Step 1: Run all tests**

Run: `go test ./internal/session/... -v`
Expected: All tests pass.

- [ ] **Step 2: Check file size**

Check that `export.go` line count grew by a reasonable amount (outline CSS+HTML+JS adds ~200 lines).

- [ ] **Step 3: Commit if everything is clean**

```bash
git add -A
git commit -m "feat: complete exported HTML outline navigation and back-to-top"
```

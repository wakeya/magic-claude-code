# Validation

## Feature Validation Checklist

### Basic

- [ ] Exported HTML has no external dependencies (no `src=` pointing to external resources in `<head>`)
- [ ] File can be opened offline (double-click loads in browser)

### Outline Panel

- [ ] Outline entry count matches user message count
- [ ] Each entry preview text is correct (max 50 chars, truncated with `...`)
- [ ] Each entry displays timestamp (format `YYYY-MM-DD HH:mm:ss`)

### Interactions

- [ ] Click outline entry → page smoothly scrolls to the message
- [ ] Scroll page → current visible message's outline entry auto-highlights
- [ ] Back-to-top button → page smoothly scrolls to top
- [ ] Small screen (<1024px): floating button is visible and clickable
- [ ] Small screen: clicking floating button opens modal
- [ ] Small screen modal: clicking outline entry navigates and closes modal

### Responsive

- [ ] At ≥1024px width: right panel is fixed and visible
- [ ] At ≤1023px width: right panel is hidden, floating button shown
- [ ] Layout updates correctly when window is resized

### Theme

- [ ] Light theme (`data-theme="light"`): outline panel styles are correct
- [ ] Dark theme (`data-theme="dark"`): outline panel styles are correct

### Performance

- [ ] File size growth <20KB
- [ ] IntersectionObserver causes no noticeable performance issues during scrolling

### Regression

- [ ] Existing export functionality unaffected (header, main messages, system/tool folding, etc.)

---

## Evidence Log

*Record actual validation results here after implementation*

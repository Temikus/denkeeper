// Applies the persisted dark-theme class before first paint to avoid a flash of
// the wrong theme. Loaded as an external classic script (not inline) so the
// dashboard can enforce a strict `script-src 'self'` Content-Security-Policy.
if (localStorage.getItem('dk_theme') === 'dark') {
  document.documentElement.classList.add('dark')
}

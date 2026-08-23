try {
  const savedTheme = localStorage.getItem('relaypulse-theme');
  document.documentElement.dataset.theme = savedTheme === 'light' || savedTheme === 'dark'
    ? savedTheme
    : (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
} catch (_) {}

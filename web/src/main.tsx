import { createRoot } from 'react-dom/client';
import '@fontsource-variable/inter-tight';
import '@fontsource-variable/geist-mono';
import '@fontsource-variable/geist';
import '@fontsource-variable/bricolage-grotesque';
import './style.css';
import './looks.css';
import { applyLook } from './look';
import App from './App';
// Set before the first paint so the page does not flash the classic look.
applyLook();
createRoot(document.getElementById('root')!).render(<App />);
// The service worker needs a secure context (HTTPS or localhost).
if (import.meta.env.PROD && 'serviceWorker' in navigator && window.isSecureContext)
  void navigator.serviceWorker.register('/sw.js').catch(() => {});

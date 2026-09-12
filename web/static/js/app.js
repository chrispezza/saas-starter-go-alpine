// Main application JavaScript

// Register Alpine.js components and stores when available
document.addEventListener('alpine:init', () => {
  // Example Alpine.js component
  Alpine.data('dropdown', () => ({
    open: false,
    toggle() {
      this.open = !this.open;
    }
  }));

  // Cross-component state for the /patterns global-store demo: unrelated
  // x-data islands read and write this via $store.demo.
  Alpine.store('demo', {
    count: 0,
    inc() {
      this.count++;
    }
  });
});

// HTMX event listeners
document.addEventListener('htmx:configRequest', (event) => {
  // Add any global request handling here
});

document.addEventListener('htmx:beforeSwap', (event) => {
  // HTMX skips swapping 4xx responses by default, but this app's handlers
  // return meaningful fragments on validation and auth failures (400/401/
  // 409/422): inline form errors, re-rendered question cards. Swap those so
  // the user sees the feedback; other statuses keep the default behavior.
  // A 429 is swapped only when the limiter's refusal is HTML (the /patterns
  // demo, ADR-034) — the plain-text default stays out of the page.
  const status = event.detail.xhr.status;
  const contentType = event.detail.xhr.getResponseHeader('Content-Type') || '';
  if ([400, 401, 409, 422].includes(status) || (status === 429 && contentType.includes('text/html'))) {
    event.detail.shouldSwap = true;
    event.detail.isError = false;
  }
});

document.addEventListener('htmx:afterSwap', (event) => {
  // Reinitialize Alpine components after HTMX content swap
  if (window.Alpine) {
    window.Alpine.initTree(event.detail.target);
  }
});

// Log when the app is fully loaded
document.addEventListener('DOMContentLoaded', () => {
  console.log('Go Performance Starter loaded');
});

// No global loading overlay: it flashed the whole screen on every fragment
// swap. In-flight feedback is per-element — htmx puts .htmx-request on the
// triggering element (styled in input.css); hx-indicator and hx-disabled-elt
// handle explicit spinners and disabled submit buttons.

// Scroll-spy for the /patterns sidebar TOC: highlight the pattern section
// currently in view. Progressive enhancement — without JS the TOC is still
// a working anchor list.
document.addEventListener('DOMContentLoaded', () => {
  const nav = document.querySelector('[data-testid="patterns-nav"]');
  if (!nav || !('IntersectionObserver' in window)) return;

  const links = new Map();
  nav.querySelectorAll('a[href^="#"]').forEach((a) => {
    links.set(a.getAttribute('href').slice(1), a);
  });

  const activate = (id) => {
    links.forEach((a, key) => {
      if (key === id) {
        a.setAttribute('aria-current', 'location');
        a.classList.add('text-link', 'font-medium');
        a.classList.remove('text-muted-foreground');
      } else {
        a.removeAttribute('aria-current');
        a.classList.remove('text-link', 'font-medium');
        a.classList.add('text-muted-foreground');
      }
    });
  };

  const observer = new IntersectionObserver(
    (entries) => {
      entries.forEach((e) => {
        if (e.isIntersecting) activate(e.target.id);
      });
    },
    // Fire when a section crosses the upper-middle band of the viewport.
    { rootMargin: '-15% 0px -70% 0px' }
  );
  links.forEach((_, id) => {
    const section = document.getElementById(id);
    if (section) observer.observe(section);
  });
});

'use strict';

// No worker: the page's security policy only allows scripts from this address.
const fire = confetti.create(document.getElementById('confetti'), { resize: true, useWorker: false });

const $ = (id) => document.getElementById(id);

let current = null;

function drawIcons(root = document) {
  root.querySelectorAll('[data-icon]').forEach((el) => {
    if (!el.firstChild) el.innerHTML = window.ICONS[el.dataset.icon] || '';
  });
}

// Pages: the hash picks one, like the section menu on the portal's pages.
const PAGES = ['welcome', 'settings', 'logs', 'updates', 'deploy'];

function showPage() {
  const page = PAGES.includes(location.hash.slice(1)) ? location.hash.slice(1) : 'welcome';
  PAGES.forEach((name) => { $(`page-${name}`).hidden = name !== page; });
  document.querySelectorAll('.nav-item').forEach((item) => {
    if (item.dataset.page === page) item.setAttribute('aria-current', 'page');
    else item.removeAttribute('aria-current');
  });
  document.title = `${$(`page-${page}`).dataset.title} · My Apps demo`;
}

function celebrate(style, accent) {
  const base = { disableForReducedMotion: true, origin: { y: 0.6 } };
  switch (style) {
    case 'off':
      return;
    case 'stars':
      fire({ ...base, particleCount: 80, spread: 100, shapes: ['star'], scalar: 1.4,
        colors: ['#facc15', '#fde68a', '#f59e0b', accent] });
      return;
    case 'snow': {
      const end = Date.now() + 2500;
      (function frame() {
        fire({ disableForReducedMotion: true, particleCount: 2, startVelocity: 0, ticks: 300,
          gravity: 0.4, scalar: 0.8, drift: Math.random() - 0.5, shapes: ['circle'],
          colors: ['#ffffff', '#e0f2fe'], origin: { x: Math.random(), y: -0.05 } });
        if (Date.now() < end) requestAnimationFrame(frame);
      })();
      return;
    }
    case 'hearts':
    case 'party': {
      const emoji = style === 'hearts' ? ['❤️', '💜', '💙'] : ['🎉', '🥳', '✨'];
      const shapes = emoji.map((text) => confetti.shapeFromText({ text, scalar: 2 }));
      fire({ ...base, particleCount: 50, spread: 100, shapes, scalar: 2, flat: true });
      return;
    }
    default:
      fire({ ...base, particleCount: 120, spread: 80, colors: [accent, '#facc15', '#f472b6', '#22d3ee', '#a3e635'] });
      fire({ ...base, particleCount: 60, angle: 60, spread: 55, origin: { x: 0, y: 0.7 } });
      fire({ ...base, particleCount: 60, angle: 120, spread: 55, origin: { x: 1, y: 0.7 } });
  }
}

// Dark text on a light colour, light text on a dark one.
function readableOn(hex) {
  const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map((c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
  return 0.2126 * r + 0.7152 * g + 0.0722 * b > 0.4 ? 'hsl(222.2 84% 4.9%)' : 'hsl(210 40% 98%)';
}

function render(info) {
  const s = info.settings;
  const rootStyle = document.documentElement.style;
  rootStyle.setProperty('--primary', s.color);
  rootStyle.setProperty('--primary-foreground', readableOn(s.color));
  $('greeting').textContent = s.greeting;

  $('application').textContent = info.application || 'We could not read this from the address.';
  $('organisation').textContent = info.organisation || 'We could not read this from the address.';
  $('address').textContent = info.address;
  $('address').href = info.address;
  $('version').textContent = info.version;
  $('version-inline').textContent = info.version;
  $('header-version').textContent = `Version ${info.version}`;
  $('started').textContent = new Date(info.startedAt).toLocaleString();

  const rows = s.list.map((item) => {
    const tr = document.createElement('tr');
    const name = document.createElement('td');
    const code = document.createElement('code');
    code.textContent = item.name;
    name.append(code);

    const now = document.createElement('td');
    if (!item.isSet) {
      const label = document.createElement('span');
      label.className = 'muted';
      label.textContent = 'Not set, so: ';
      now.append(label, item.used);
    } else if (!item.accepted) {
      now.textContent = item.used;
      const warn = document.createElement('div');
      warn.className = 'warn';
      warn.textContent = `We did not recognise "${item.value}", so we use the default.`;
      now.append(warn);
    } else {
      now.textContent = item.used;
    }

    const effect = document.createElement('td');
    effect.textContent = item.effect;
    const options = document.createElement('div');
    options.className = 'muted small';
    options.textContent = `Options: ${item.options}`;
    effect.append(options);

    tr.append(name, now, effect);
    return tr;
  });
  $('settings').replaceChildren(...rows);

  $('notes').replaceChildren(...info.notes.map((note) => {
    const li = document.createElement('li');
    li.textContent = note;
    return li;
  }));
  const next = info.versions.find((v) => v !== info.version) || info.versions[0];
  $('next-version').textContent = next;
  $('rollback-hint').hidden = info.version !== info.versions[info.versions.length - 1];
}

function toast(title, text) {
  const el = document.createElement('div');
  el.className = 'toast';
  el.innerHTML = '<span data-icon="check"></span><div><div class="toast-title"></div><div class="toast-text"></div></div>';
  el.querySelector('.toast-title').textContent = title;
  el.querySelector('.toast-text').textContent = text;
  drawIcons(el);
  $('toaster').append(el);
  setTimeout(() => el.remove(), 8000);
}

function changesBetween(before, after) {
  const changes = [];
  if (before.version !== after.version) changes.push(`version ${after.version}`);
  if (JSON.stringify(before.settings) !== JSON.stringify(after.settings)) changes.push('your new settings');
  return changes;
}

async function loadInfo() {
  const response = await fetch('/api/info', { cache: 'no-store' });
  if (!response.ok) throw new Error(String(response.status));
  return response.json();
}

// Watch for an update. During one, the old and the new version answer side
// by side for a moment, so only a change away from what we show counts.
async function poll() {
  try {
    const info = await loadInfo();
    const changes = changesBetween(current, info);
    if (changes.length > 0) {
      current = info;
      render(info);
      toast('Your update is live', `This page now runs ${changes.join(' and ')}.`);
      celebrate(info.settings.confetti, info.settings.color);
    }
  } catch {
    // The application may be restarting during an update. Try again next round.
  }
}

$('celebrate').addEventListener('click', () => {
  if (current) celebrate(current.settings.confetti, current.settings.color);
});

window.addEventListener('hashchange', showPage);

$('log-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const input = $('log-message');
  $('log-error').hidden = true;
  try {
    const response = await fetch('/api/log', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ message: input.value }),
    });
    const body = await response.json();
    if (!response.ok) throw new Error(body.error);
    $('log-line').textContent = body.message;
    $('log-result').hidden = false;
    input.value = '';
  } catch (error) {
    $('log-error').textContent = error.message || 'We could not write the line just now. Try again.';
    $('log-error').hidden = false;
  }
});

drawIcons();
showPage();

(async () => {
  try {
    current = await loadInfo();
    render(current);
    celebrate(current.settings.confetti, current.settings.color);
  } catch {
    $('greeting').textContent = 'We could not load the details just now. Refresh the page to try again.';
  }
  setInterval(poll, 5000);
})();

(() => {
  const $ = (id) => document.getElementById(id);
  const MAX_MESSAGE_BYTES = 4096; // defaultMaxMessageSize in internal/chat/room.go

  const CATS = [
    { name: 'Caras', icon: '😀', list: '😀 😃 😄 😁 😆 😅 😂 🤣 🙂 😉 😊 😇 🥰 😍 🤩 😘 😋 😛 😜 🤪 🤔 🤨 😐 😑 😶 🙄 😏 😴 😮 😲 😢 😭 😤 😡 🥳 😎 🤓 🥺 😱 🤯 😬 🫠 🤗 🫡' },
    { name: 'Gestos', icon: '👍', list: '👍 👎 👌 ✌️ 🤞 🤟 🤘 🤙 👈 👉 👆 👇 👋 🤚 ✋ 🖖 👏 🙌 🫶 🙏 🤝 💪 ✍️ 🫰 🫵 👀 🧠 💀 👻 🤖' },
    { name: 'Corazones y símbolos', icon: '❤️', list: '❤️ 🧡 💛 💚 💙 💜 🖤 🤍 💔 💕 💖 💯 ✨ ⭐ 🔥 💥 ⚡ 🎉 🎊 ✅ ❌ ❓ ❗ 💡 💬 🔔' },
    { name: 'Cosas', icon: '🍕', list: '🍕 🍔 🍟 🌮 🍣 🍩 🍪 🍫 ☕ 🍺 🍷 🎂 ⚽ 🏀 🎮 🎧 🎸 🚀 🌈 🌙 ☀️ 🌧️ 🐶 🐱 🦊 🐼 🐸 🦄' },
  ];
  const STATUS_LABEL = { connecting: 'Conectando…', online: 'En línea', reconnecting: 'Reconectando…' };
  // Mirrors imageHosts in cmd/chatroom/main.go (CSP img-src). Keep both in sync.
  const IMAGE_HOSTS = new Set([
    'media.giphy.com', 'media0.giphy.com', 'media1.giphy.com', 'media2.giphy.com',
    'media3.giphy.com', 'media4.giphy.com', 'i.giphy.com',
    'media.tenor.com', 'i.imgur.com',
  ]);
  const IMAGE_PATH = /\.(gif|webp|png|jpe?g)$/i;
  const RETRY_BASE_MS = 1000;
  const RETRY_MAX_MS = 30000;
  const EMOJI_ONLY = /^(\p{Extended_Pictographic}|️|‍|\s){1,12}$/u;

  let ws = null;
  let myName = ''; // identity assigned by the server in the welcome frame
  let status = 'connecting';
  let lastAuthor = null;
  let requestedNick = ''; // what the user typed; re-sent on every reconnect
  let retryTimer = null; // pending reconnect timeout id, or null
  let attempt = 0; // consecutive failed attempts since the last welcome
  let outage = false; // a "connection lost" notice is on screen
  let users = new Set(); // nicknames in the room; the server's users frame is authoritative

  // True only when the whole message is one https image URL on an allowlisted host.
  function isAllowedImage(text) {
    if (/\s/.test(text)) return false;
    let url;
    try { url = new URL(text); } catch { return false; }
    return url.protocol === 'https:' && IMAGE_HOSTS.has(url.hostname) &&
      url.port === '' && url.username === '' && url.password === '' &&
      url.hash === '' && IMAGE_PATH.test(url.pathname);
  }

  // — join —
  const nickInput = $('nick');
  nickInput.addEventListener('input', () => { $('join-btn').disabled = !nickInput.value.trim(); });
  $('join-form').addEventListener('submit', (e) => {
    e.preventDefault();
    const nick = nickInput.value.trim();
    if (nick) join(nick);
  });

  function join(nick) {
    clearTimeout(retryTimer);
    retryTimer = null;
    requestedNick = nick;
    attempt = 0;
    outage = false;
    myName = '';
    lastAuthor = null;
    users = new Set();
    renderUsers();
    $('messages').replaceChildren();
    $('me').textContent = nick;
    $('me').title = nick;
    $('join-view').hidden = true;
    $('chat-view').hidden = false;
    setStatus('connecting');
    $('draft').focus();
    connect();
  }

  // No onopen handler: the page is online only once the welcome frame arrives.
  function connect() {
    retryTimer = null;
    const scheme = location.protocol === 'https:' ? 'wss://' : 'ws://';
    const socket = new WebSocket(scheme + location.host + '/ws?nickname=' + encodeURIComponent(requestedNick));
    ws = socket;
    socket.onmessage = (e) => {
      if (ws !== socket) return; // late frame from a socket the user already left
      addRaw(e.data);
    };
    socket.onclose = () => {
      if (ws !== socket) return; // the user left on purpose
      scheduleReconnect();
    };
  }

  // Exponential backoff with equal jitter: 0.5-1 s, 1-2 s, 2-4 s ... 15-30 s.
  function scheduleReconnect() {
    ws = null;
    setStatus('reconnecting');
    if (!outage) {
      outage = true;
      addSystem('Conexión perdida, reconectando…');
    }
    const d = Math.min(RETRY_MAX_MS, RETRY_BASE_MS * 2 ** attempt);
    attempt++;
    retryTimer = setTimeout(connect, d / 2 + Math.random() * d / 2);
  }

  // Cuts a pending wait short, or replaces a retry socket stuck connecting (the browser
  // can take a minute to give up on it). Never leaves two sockets open.
  function reconnectNow() {
    if (retryTimer !== null) {
      clearTimeout(retryTimer);
    } else if (status === 'reconnecting' && ws && ws.readyState === WebSocket.CONNECTING) {
      const s = ws; ws = null; s.close(); // ws = null makes its onclose a no-op
    } else {
      return;
    }
    connect();
  }
  window.addEventListener('online', reconnectNow);
  document.addEventListener('visibilitychange', () => { if (!document.hidden) reconnectNow(); });

  $('leave-btn').addEventListener('click', () => {
    clearTimeout(retryTimer);
    retryTimer = null;
    if (ws) { const s = ws; ws = null; s.close(); }
    closePanels();
    $('draft').value = '';
    $('chat-view').hidden = true;
    $('join-view').hidden = false;
    nickInput.focus();
  });

  function setStatus(s) {
    status = s;
    $('status').dataset.state = s;
    $('status-label').textContent = STATUS_LABEL[s];
    updateSendState();
  }

  // — messages —
  // The server sends JSON frames {type, from, text, ts}; ts is Unix milliseconds.
  function addRaw(data) {
    let msg;
    try { msg = JSON.parse(data); } catch { return; }
    if (!msg) return;
    // users and history carry no from, so they are handled before that guard.
    if (msg.type === 'users') {
      if (Array.isArray(msg.users)) {
        users = new Set(msg.users.filter((n) => typeof n === 'string'));
        renderUsers();
      }
      return;
    }
    if (msg.type === 'history') {
      if (Array.isArray(msg.messages)) renderHistory(msg.messages);
      return;
    }
    if (typeof msg.from !== 'string') return;
    switch (msg.type) {
      case 'welcome':
        myName = msg.from;
        $('me').textContent = myName;
        $('me').title = myName;
        attempt = 0;
        setStatus('online');
        break;
      case 'chat':
        if (typeof msg.text === 'string') addChat(msg.from, msg.text, msg.ts);
        break;
      case 'join':
        users.add(msg.from);
        renderUsers();
        addSystem(msg.from + ' se unió 🎉');
        break;
      case 'leave':
        users.delete(msg.from);
        renderUsers();
        addSystem(msg.from + ' salió 👋');
        break;
      // Unknown types are ignored so newer servers don't break this page.
    }
  }

  // The history frame replaces the whole transcript, so a reconnect never duplicates
  // messages. "Reconectado" goes after it, or the replacement would wipe it.
  function renderHistory(messages) {
    const list = $('messages');
    list.setAttribute('aria-busy', 'true');
    try {
      list.replaceChildren();
      lastAuthor = null;
      for (const m of messages) {
        if (m && m.type === 'chat' && typeof m.from === 'string' && typeof m.text === 'string') {
          addChat(m.from, m.text, m.ts);
        }
      }
    } finally {
      list.removeAttribute('aria-busy');
    }
    if (outage) {
      outage = false;
      addSystem('Reconectado');
    }
  }

  function renderUsers() {
    const names = [...users].sort((a, b) => a.localeCompare(b, 'es', { sensitivity: 'base' }));
    $('users-count').textContent = String(users.size);
    $('users-list').replaceChildren(...names.map((name) => {
      const li = document.createElement('li');
      const label = document.createElement('span');
      label.textContent = name;
      li.append(label);
      if (name === myName) {
        const tag = document.createElement('span');
        tag.className = 'tag tag-accent';
        tag.textContent = 'vos';
        li.append(tag);
      }
      return li;
    }));
  }

  function addSystem(text) {
    lastAuthor = null;
    const li = document.createElement('li');
    li.className = 'msg-system';
    li.textContent = text;
    append(li);
  }

  function addChat(author, text, ts) {
    const own = author === myName;
    const showHead = author !== lastAuthor;
    lastAuthor = author;

    const li = document.createElement('li');
    li.className = 'msg' + (own ? ' own' : '') + (showHead ? ' has-head' : '');

    const slot = document.createElement('div');
    slot.className = 'avatar-slot';
    const body = document.createElement('div');
    body.className = 'msg-body';

    if (showHead) {
      const avatar = document.createElement('div');
      avatar.className = 'avatar';
      avatar.setAttribute('aria-hidden', 'true');
      avatar.textContent = (Array.from(author)[0] || '?').toUpperCase();
      slot.append(avatar);

      const head = document.createElement('div');
      head.className = 'msg-head';
      const name = document.createElement('span');
      name.className = 'author';
      name.textContent = author;
      head.append(name);
      if (own) {
        const tag = document.createElement('span');
        tag.className = 'tag tag-accent';
        tag.textContent = 'vos';
        head.append(tag);
      }
      const time = document.createElement('time');
      time.className = 'text-muted';
      time.textContent = (Number.isFinite(ts) ? new Date(ts) : new Date()).toLocaleTimeString('es', { hour: '2-digit', minute: '2-digit' });
      head.append(time);
      body.append(head);
    }

    const bubble = document.createElement('div');
    bubble.className = 'bubble';
    if (isAllowedImage(text)) {
      bubble.classList.add('media');
      const img = document.createElement('img');
      img.src = text;
      img.alt = 'GIF';
      img.loading = 'lazy';
      img.referrerPolicy = 'no-referrer';
      img.onload = scrollToBottom;
      // Fall back to the plain link text if the image can't be loaded.
      img.onerror = () => { bubble.classList.remove('media'); bubble.textContent = text; };
      bubble.append(img);
    } else {
      if (EMOJI_ONLY.test(text)) bubble.classList.add('emoji-only');
      bubble.textContent = text;
    }
    body.append(bubble);

    li.append(slot, body);
    append(li);
  }

  function append(node) {
    $('messages').append(node);
    scrollToBottom();
  }

  function scrollToBottom() {
    const el = $('scroller');
    el.scrollTop = el.scrollHeight;
  }

  // — sending —
  const draft = $('draft');
  draft.addEventListener('input', updateSendState);

  function updateSendState() {
    $('send-btn').disabled = !draft.value.trim() || status !== 'online';
    $('gif-send').disabled = !isAllowedImage($('gif-url').value.trim()) || status !== 'online';
  }

  function sendText(text) {
    if (status !== 'online' || !ws || ws.readyState !== WebSocket.OPEN) return false;
    if (new TextEncoder().encode(text).length > MAX_MESSAGE_BYTES) {
      addSystem('El mensaje es demasiado largo.');
      return false;
    }
    ws.send(text);
    return true;
  }

  $('send-form').addEventListener('submit', (e) => {
    e.preventDefault();
    const text = draft.value.trim();
    if (text && sendText(text)) {
      draft.value = '';
      closePanels();
      updateSendState();
    }
  });

  // — emoji & GIF panels —
  const panels = {
    emoji: { panel: $('emoji-panel'), btn: $('emoji-btn') },
    gif: { panel: $('gif-panel'), btn: $('gif-btn') },
    users: { panel: $('users-panel'), btn: $('users-btn') },
  };

  function togglePanel(key) {
    const opening = panels[key].panel.hidden;
    closePanels();
    if (!opening) return;
    panels[key].panel.hidden = false;
    panels[key].btn.setAttribute('aria-expanded', 'true');
    if (key === 'gif') $('gif-url').focus();
  }

  function closePanels() {
    for (const { panel, btn } of Object.values(panels)) {
      panel.hidden = true;
      btn.setAttribute('aria-expanded', 'false');
    }
  }

  panels.emoji.btn.addEventListener('click', () => togglePanel('emoji'));
  panels.gif.btn.addEventListener('click', () => togglePanel('gif'));
  panels.users.btn.addEventListener('click', () => togglePanel('users'));
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape') closePanels(); });
  document.addEventListener('click', (e) => {
    if (!e.target.closest('.popover, .tool')) closePanels();
  });

  CATS.forEach((cat, i) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.role = 'tab';
    b.textContent = cat.icon;
    b.title = cat.name;
    b.setAttribute('aria-label', cat.name);
    b.addEventListener('click', () => selectCat(i));
    $('cats').append(b);
  });

  function selectCat(i) {
    [...$('cats').children].forEach((b, j) => b.setAttribute('aria-selected', String(i === j)));
    const grid = $('emoji-grid');
    grid.replaceChildren(...CATS[i].list.split(' ').map((ch) => {
      const b = document.createElement('button');
      b.type = 'button';
      b.textContent = ch;
      b.addEventListener('click', () => {
        draft.value += ch;
        updateSendState();
      });
      return b;
    }));
  }
  selectCat(0);

  const gifUrl = $('gif-url');
  gifUrl.addEventListener('input', updateSendState);
  gifUrl.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); $('gif-send').click(); } });
  $('gif-send').addEventListener('click', () => {
    const url = gifUrl.value.trim();
    if (isAllowedImage(url) && sendText(url)) {
      gifUrl.value = '';
      updateSendState();
      closePanels();
    }
  });
})();

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

  const rooms = new Map(); // id -> room, in /rooms order
  let active = null; // the room whose pane and header are shown
  let requestedNick = ''; // what the user typed; re-sent by every room's socket

  // True only when the whole message is one https image URL on an allowlisted host.
  function isAllowedImage(text) {
    if (/\s/.test(text)) return false;
    let url;
    try { url = new URL(text); } catch { return false; }
    return url.protocol === 'https:' && IMAGE_HOSTS.has(url.hostname) &&
      url.port === '' && url.username === '' && url.password === '' &&
      url.hash === '' && IMAGE_PATH.test(url.pathname);
  }

  // — rooms —
  // The server's room list is the single source of truth. Ids end up in element ids and
  // URLs, so anything outside this shape is dropped.
  const ROOM_ID = /^[a-z0-9-]{1,32}$/;

  async function loadRooms() {
    const res = await fetch('/rooms', { cache: 'no-store' });
    if (!res.ok) throw new Error('rooms: ' + res.status);
    const body = await res.json();
    if (!body || !Array.isArray(body.rooms)) throw new Error('rooms: bad body');
    const ids = body.rooms.filter((id) => typeof id === 'string' && ROOM_ID.test(id));
    if (ids.length === 0) throw new Error('rooms: empty');
    for (const id of new Set(ids)) createRoom(id);
  }

  // One state object per room; its socket handlers close over it.
  function createRoom(id) {
    const tab = document.createElement('button');
    tab.type = 'button';
    tab.id = 'tab-' + id;
    tab.className = 'room-tab';
    tab.setAttribute('role', 'tab');
    tab.setAttribute('aria-selected', 'false');
    tab.setAttribute('aria-controls', 'scroller');
    const nameEl = document.createElement('span');
    nameEl.className = 'room-tab-name';
    nameEl.textContent = '#' + id;
    const unreadEl = document.createElement('span');
    unreadEl.className = 'room-unread';
    unreadEl.hidden = true;
    tab.append(nameEl, unreadEl);

    const list = document.createElement('ol');
    list.id = 'pane-' + id;
    list.className = 'messages';
    list.setAttribute('aria-live', 'polite');
    list.hidden = true;

    const room = {
      id, tab, unreadEl, list,
      joined: false, ws: null, retryTimer: null, attempt: 0, outage: false,
      status: 'connecting', myName: '', users: new Set(), lastAuthor: null, unread: 0,
    };
    tab.addEventListener('click', () => {
      if (room === active) return;
      if (!room.joined) joinRoom(room);
      activate(room);
    });
    rooms.set(id, room);
    $('room-tabs').append(tab);
    $('scroller').append(list);
    renderTab(room);
    return room;
  }

  function renderTab(room) {
    room.tab.classList.toggle('joined', room.joined);
    room.unreadEl.hidden = room.unread === 0;
    room.unreadEl.textContent = room.unread > 99 ? '99+' : String(room.unread);
    let label = '#' + room.id;
    if (!room.joined) label += ', sin unirte';
    if (room.unread > 0) label += ', ' + room.unread + ' sin leer';
    room.tab.setAttribute('aria-label', label);
  }

  function bumpUnread(room) {
    room.unread++;
    renderTab(room);
  }

  function joinedRooms() {
    return [...rooms.values()].filter((r) => r.joined);
  }

  function updateLeaveBtn() {
    $('room-leave-btn').disabled = joinedRooms().length <= 1;
  }

  function joinRoom(room) {
    room.joined = true;
    room.list.replaceChildren();
    room.users = new Set();
    room.myName = '';
    room.lastAuthor = null;
    room.attempt = 0;
    room.outage = false;
    room.unread = 0;
    setStatus(room, 'connecting');
    connect(room);
    renderTab(room);
    updateLeaveBtn();
  }

  // Makes the room's pane and header current. Focus and the draft are left alone.
  function activate(room) {
    active = room;
    for (const r of rooms.values()) {
      r.tab.setAttribute('aria-selected', String(r === room));
      r.list.hidden = r !== room;
    }
    $('scroller').setAttribute('aria-labelledby', room.tab.id);
    $('room-name').textContent = '#' + room.id;
    const me = room.myName || requestedNick;
    $('me').textContent = me;
    $('me').title = me;
    showStatus(room);
    room.unread = 0;
    renderTab(room);
    renderUsers();
    scrollToBottom();
    updateLeaveBtn();
  }

  function teardown(room) {
    clearTimeout(room.retryTimer);
    room.retryTimer = null;
    if (room.ws) { const s = room.ws; room.ws = null; s.close(); } // ws = null makes its onclose a no-op
    room.joined = false;
    room.list.replaceChildren();
    room.users = new Set();
    room.myName = '';
    room.lastAuthor = null;
    room.unread = 0;
    room.outage = false;
    renderTab(room);
  }

  // — join —
  const nickInput = $('nick');
  const roomsReady = loadRooms();
  roomsReady.catch(() => {}); // handled on submit; avoids an unhandled rejection
  let joining = false;
  nickInput.addEventListener('input', () => { $('join-btn').disabled = !nickInput.value.trim(); });
  $('join-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const nick = nickInput.value.trim();
    if (!nick || joining) return;
    joining = true;
    try {
      if (rooms.size === 0) {
        try {
          await roomsReady;
        } catch {
          await loadRooms(); // one retry
        }
      }
    } catch {
      $('join-error').textContent = 'No pudimos cargar las salas. Probá de nuevo.';
      $('join-error').hidden = false;
      return;
    } finally {
      joining = false;
    }
    $('join-error').hidden = true;
    enterChat(nick);
  });

  function enterChat(nick) {
    requestedNick = nick;
    const first = rooms.values().next().value;
    $('join-view').hidden = true;
    $('chat-view').hidden = false;
    joinRoom(first);
    activate(first);
    $('draft').focus();
  }

  // No onopen handler: a room is online only once its welcome frame arrives.
  function connect(room) {
    room.retryTimer = null;
    const scheme = location.protocol === 'https:' ? 'wss://' : 'ws://';
    const socket = new WebSocket(scheme + location.host + '/ws?room=' + encodeURIComponent(room.id) +
      '&nickname=' + encodeURIComponent(requestedNick));
    room.ws = socket;
    socket.onmessage = (e) => {
      if (room.ws !== socket) return; // late frame from a socket the user already left
      addRaw(room, e.data);
    };
    socket.onclose = () => {
      if (room.ws !== socket) return; // the user left on purpose
      scheduleReconnect(room);
    };
  }

  // Exponential backoff with equal jitter: 0.5-1 s, 1-2 s, 2-4 s ... 15-30 s.
  function scheduleReconnect(room) {
    room.ws = null;
    setStatus(room, 'reconnecting');
    if (!room.outage) {
      room.outage = true;
      addSystem(room, 'Conexión perdida, reconectando…');
    }
    const d = Math.min(RETRY_MAX_MS, RETRY_BASE_MS * 2 ** room.attempt);
    room.attempt++;
    room.retryTimer = setTimeout(() => connect(room), d / 2 + Math.random() * d / 2);
  }

  // Cuts a pending wait short, or replaces a retry socket stuck connecting (the browser
  // can take a minute to give up on it). Never leaves two sockets open for a room.
  function reconnectNow(room) {
    if (room.retryTimer !== null) {
      clearTimeout(room.retryTimer);
    } else if (room.status === 'reconnecting' && room.ws && room.ws.readyState === WebSocket.CONNECTING) {
      const s = room.ws; room.ws = null; s.close(); // ws = null makes its onclose a no-op
    } else {
      return;
    }
    connect(room);
  }
  function reconnectAll() { joinedRooms().forEach(reconnectNow); }
  window.addEventListener('online', reconnectAll);
  document.addEventListener('visibilitychange', () => { if (!document.hidden) reconnectAll(); });

  $('leave-btn').addEventListener('click', () => {
    rooms.forEach((room) => { if (room.joined) teardown(room); });
    updateLeaveBtn();
    closePanels();
    $('draft').value = '';
    $('chat-view').hidden = true;
    $('join-view').hidden = false;
    nickInput.focus();
  });

  $('room-leave-btn').addEventListener('click', () => {
    if (!active || joinedRooms().length <= 1) return;
    teardown(active);
    activate(joinedRooms()[0]);
  });

  function setStatus(room, s) {
    room.status = s;
    if (room === active) showStatus(room);
  }

  function showStatus(room) {
    $('status').dataset.state = room.status;
    $('status-label').textContent = STATUS_LABEL[room.status];
    updateSendState();
  }

  // — messages —
  // The server sends JSON frames {type, from, text, ts}; ts is Unix milliseconds.
  // Frames carry no room: the socket (and so the closure) identifies it.
  function addRaw(room, data) {
    let msg;
    try { msg = JSON.parse(data); } catch { return; }
    if (!msg) return;
    // users and history carry no from, so they are handled before that guard.
    if (msg.type === 'users') {
      if (Array.isArray(msg.users)) {
        room.users = new Set(msg.users.filter((n) => typeof n === 'string'));
        if (room === active) renderUsers();
      }
      return;
    }
    if (msg.type === 'history') {
      if (Array.isArray(msg.messages)) renderHistory(room, msg.messages);
      return;
    }
    if (typeof msg.from !== 'string') return;
    switch (msg.type) {
      case 'welcome':
        room.myName = msg.from;
        if (room === active) {
          $('me').textContent = room.myName;
          $('me').title = room.myName;
          renderUsers();
        }
        room.attempt = 0;
        setStatus(room, 'online');
        break;
      case 'chat':
        if (typeof msg.text === 'string') {
          addChat(room, msg.from, msg.text, msg.ts);
          if (room !== active && msg.from !== room.myName) bumpUnread(room);
        }
        break;
      case 'join':
        room.users.add(msg.from);
        if (room === active) renderUsers();
        addSystem(room, msg.from + ' se unió 🎉');
        break;
      case 'leave':
        room.users.delete(msg.from);
        if (room === active) renderUsers();
        addSystem(room, msg.from + ' salió 👋');
        break;
      // Unknown types are ignored so newer servers don't break this page.
    }
  }

  // The history frame replaces the whole transcript, so a reconnect never duplicates
  // messages. "Reconectado" goes after it, or the replacement would wipe it.
  function renderHistory(room, messages) {
    const list = room.list;
    list.setAttribute('aria-busy', 'true');
    try {
      list.replaceChildren();
      room.lastAuthor = null;
      for (const m of messages) {
        if (m && m.type === 'chat' && typeof m.from === 'string' && typeof m.text === 'string') {
          addChat(room, m.from, m.text, m.ts);
        }
      }
    } finally {
      list.removeAttribute('aria-busy');
    }
    if (room.outage) {
      room.outage = false;
      addSystem(room, 'Reconectado');
    }
  }

  // Renders the active room's users; "vos" is that room's assigned name.
  function renderUsers() {
    if (!active) return;
    const names = [...active.users].sort((a, b) => a.localeCompare(b, 'es', { sensitivity: 'base' }));
    $('users-count').textContent = String(active.users.size);
    $('users-list').replaceChildren(...names.map((name) => {
      const li = document.createElement('li');
      const label = document.createElement('span');
      label.textContent = name;
      li.append(label);
      if (name === active.myName) {
        const tag = document.createElement('span');
        tag.className = 'tag tag-accent';
        tag.textContent = 'vos';
        li.append(tag);
      }
      return li;
    }));
  }

  function addSystem(room, text) {
    room.lastAuthor = null;
    const li = document.createElement('li');
    li.className = 'msg-system';
    li.textContent = text;
    append(room, li);
  }

  function addChat(room, author, text, ts) {
    const own = author === room.myName;
    const showHead = author !== room.lastAuthor;
    room.lastAuthor = author;

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
      img.onload = () => { if (room === active) scrollToBottom(); };
      // Fall back to the plain link text if the image can't be loaded.
      img.onerror = () => { bubble.classList.remove('media'); bubble.textContent = text; };
      bubble.append(img);
    } else {
      if (EMOJI_ONLY.test(text)) bubble.classList.add('emoji-only');
      bubble.textContent = text;
    }
    body.append(bubble);

    li.append(slot, body);
    append(room, li);
  }

  function append(room, node) {
    room.list.append(node);
    if (room === active) scrollToBottom();
  }

  function scrollToBottom() {
    const el = $('scroller');
    el.scrollTop = el.scrollHeight;
  }

  // — sending —
  const draft = $('draft');
  draft.addEventListener('input', updateSendState);

  function updateSendState() {
    const online = !!active && active.status === 'online';
    $('send-btn').disabled = !draft.value.trim() || !online;
    $('gif-send').disabled = !isAllowedImage($('gif-url').value.trim()) || !online;
  }

  function sendText(text) {
    const room = active;
    if (!room || room.status !== 'online' || !room.ws || room.ws.readyState !== WebSocket.OPEN) return false;
    if (new TextEncoder().encode(text).length > MAX_MESSAGE_BYTES) {
      addSystem(room, 'El mensaje es demasiado largo.');
      return false;
    }
    room.ws.send(text);
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

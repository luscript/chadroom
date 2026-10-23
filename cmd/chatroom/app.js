(() => {
  const $ = (id) => document.getElementById(id);
  const MAX_MESSAGE_BYTES = 4096; // defaultMaxMessageSize in internal/chat/room.go
  const MAX_AVATAR_BYTES = 512; // mirrors maxAvatarBytes in cmd/chatroom/main.go
  const AVATAR_KEY = 'chadroom.avatar'; // localStorage; best effort

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
  const TYPING_SEND_MS = 3000; // at most one outgoing typing signal per pane
  const TYPING_TTL_MS = 5000; // a typist disappears this long after their latest signal
  const EMOJI_ONLY = /^(\p{Extended_Pictographic}|️|‍|\s){1,12}$/u;

  const rooms = new Map(); // id -> room, in /rooms order
  let active = null; // the room whose pane and header are shown
  let requestedNick = ''; // what the user typed; re-sent by every room's socket
  let requestedAvatar = ''; // validated join value ('' = none); re-sent by every room's socket
  let profileUrl = ''; // what #profile-avatar currently shows

  // Direct messages. A conversation rides its room's socket; the page is the only place
  // its transcript lives. Nicknames go into the DOM only as text, never into ids or classes.
  const conversations = new Map(); // convKey(room, peer) -> conversation
  let activeConv = null; // null, or a conversation of `active`: which pane is shown
  let convSeq = 0; // element ids: 'dm-tab-' + n / 'dm-pane-' + n
  // Assigned nicknames are unique per room and `\n` is in neither part, so exact equality is enough.
  const convKey = (room, nick) => room.id + '\n' + nick;
  const current = () => activeConv || active; // the pane on screen
  const ownerRoom = (pane) => pane.room || pane; // conversation -> its room; room -> itself
  let typingPane = null; // pane our last typing signal was for; null = the next edit signals at once
  let typingSentAt = 0; // performance.now() of that signal

  // True only when the whole message is one https image URL on an allowlisted host.
  function isAllowedImage(text) {
    if (/\s/.test(text)) return false;
    let url;
    try { url = new URL(text); } catch { return false; }
    return url.protocol === 'https:' && IMAGE_HOSTS.has(url.hostname) &&
      url.port === '' && url.username === '' && url.password === '' &&
      url.hash === '' && IMAGE_PATH.test(url.pathname);
  }

  // '' (no picture) or an allowed image URL that fits the server's byte limit.
  // The server compares the host exactly (CHAT-28) while new URL() normalizes case, default port
  // and IDN, so the raw text must also start with the URL's own https://hostname/.
  function validAvatarInput(v) {
    if (v === '') return true;
    if (!isAllowedImage(v) || new TextEncoder().encode(v).length > MAX_AVATAR_BYTES) return false;
    return v.startsWith('https://' + new URL(v).hostname + '/');
  }

  function letterAvatar(name) {
    const avatar = document.createElement('div');
    avatar.className = 'avatar';
    avatar.setAttribute('aria-hidden', 'true');
    avatar.textContent = (Array.from(name)[0] || '?').toUpperCase();
    return avatar;
  }

  // The picture when url is an allowed image, else the letter. The server validates too, but the
  // page never requests a URL it has not checked itself.
  function avatarEl(name, url) {
    if (typeof url !== 'string' || !isAllowedImage(url)) return letterAvatar(name);
    const img = document.createElement('img');
    img.className = 'avatar';
    img.alt = '';
    img.setAttribute('aria-hidden', 'true');
    img.referrerPolicy = 'no-referrer';
    img.loading = 'lazy';
    img.decoding = 'async';
    img.addEventListener('error', () => img.replaceWith(letterAvatar(name)));
    img.src = url;
    return img;
  }

  // The "MI PERFIL" card shows the active room's own picture; on error or none, the placeholder stays.
  function renderProfile() {
    const url = (active && active.avatars.get(active.myName)) || '';
    if (url === profileUrl) return;
    profileUrl = url;
    const img = $('profile-avatar');
    img.hidden = !url;
    if (url) img.src = url; else img.removeAttribute('src');
  }
  $('profile-avatar').addEventListener('error', () => { $('profile-avatar').hidden = true; });

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
      avatars: new Map(), // nick -> image url, only for members with a picture
      typing: new Map(), // nick -> timeout id of who is typing here now
    };
    tab.addEventListener('click', () => {
      if (room === active && !activeConv) return;
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

  // — conversations —
  function createConversation(room, peer) {
    const n = ++convSeq;
    const tab = document.createElement('button');
    tab.type = 'button';
    tab.id = 'dm-tab-' + n;
    tab.className = 'room-tab dm-tab joined';
    tab.setAttribute('role', 'tab');
    tab.setAttribute('aria-selected', 'false');
    tab.setAttribute('aria-controls', 'scroller');
    const nameEl = document.createElement('span');
    nameEl.className = 'room-tab-name';
    nameEl.textContent = '@' + peer;
    const unreadEl = document.createElement('span');
    unreadEl.className = 'room-unread';
    unreadEl.hidden = true;
    tab.append(nameEl, unreadEl);

    const list = document.createElement('ol');
    list.id = 'dm-pane-' + n;
    list.className = 'messages';
    list.setAttribute('aria-live', 'polite');
    list.hidden = true;

    const conv = { room, peer, tab, unreadEl, list, open: true, unread: 0, lastAuthor: null, typing: new Map() };
    tab.addEventListener('click', () => {
      if (conv !== activeConv) activate(room, conv);
    });
    // Right after the room's tab, or after its latest DM tab (Map order is creation order).
    let anchor = room.tab;
    for (const c of conversations.values()) if (c.room === room) anchor = c.tab;
    anchor.after(tab);
    conversations.set(convKey(room, peer), conv);
    $('scroller').append(list);
    renderConvTab(conv);
    return conv;
  }

  function renderConvTab(conv) {
    conv.tab.classList.toggle('closed', !conv.open);
    conv.unreadEl.hidden = conv.unread === 0;
    conv.unreadEl.textContent = conv.unread > 99 ? '99+' : String(conv.unread);
    let label = '@' + conv.peer + ', privado en #' + conv.room.id;
    if (!conv.open) label += ', desconectado';
    if (conv.unread > 0) label += ', ' + conv.unread + ' sin leer';
    conv.tab.setAttribute('aria-label', label);
  }

  function renderPaneTab(pane) {
    if (pane.room) renderConvTab(pane); else renderTab(pane);
  }

  // Popover action: only for someone who is in the room right now.
  function openConversation(room, peer) {
    if (!room.joined || !room.users.has(peer) || peer === room.myName) return;
    const conv = conversations.get(convKey(room, peer)) || createConversation(room, peer);
    if (!conv.open) reopenConversation(conv);
    activate(room, conv);
    closePanels();
    $('draft').focus();
  }

  // The same nickname may now belong to someone else, so the divider says so.
  function reopenConversation(conv) {
    conv.open = true;
    addSystem(conv, 'Nueva conversación con ' + conv.peer + ': puede ser otra persona.');
    renderConvTab(conv);
    if (conv === activeConv) updateSendState();
  }

  function closeConversation(conv, notice) {
    if (!conv.open) return;
    conv.open = false;
    clearAllTyping(conv);
    addSystem(conv, notice);
    renderConvTab(conv);
    if (conv === activeConv) updateSendState();
  }

  function closeConversations(room, notice) {
    for (const conv of conversations.values()) if (conv.room === room) closeConversation(conv, notice);
  }

  function removeConversations(room) {
    for (const [key, conv] of conversations) {
      if (conv.room !== room) continue;
      clearAllTyping(conv);
      conv.tab.remove();
      conv.list.remove();
      conversations.delete(key);
      if (activeConv === conv) activeConv = null;
    }
  }

  function bumpUnread(pane) {
    pane.unread++;
    renderPaneTab(pane);
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
    room.avatars = new Map();
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

  // Makes the room's pane (or one of its conversations) and header current. Focus and the draft are left alone.
  function activate(room, conv = null) {
    active = room;
    activeConv = conv;
    const shown = current();
    for (const pane of [...rooms.values(), ...conversations.values()]) {
      pane.tab.setAttribute('aria-selected', String(pane === shown));
      pane.list.hidden = pane !== shown;
    }
    $('scroller').setAttribute('aria-labelledby', shown.tab.id);
    $('room-name').textContent = conv ? '@' + conv.peer + ' · #' + room.id : '#' + room.id;
    const me = room.myName || requestedNick;
    $('me').textContent = me;
    $('profile-nick').textContent = me;
    $('me').title = me;
    showStatus(room);
    shown.unread = 0;
    renderPaneTab(shown);
    renderUsers();
    renderProfile();
    scrollToBottom();
    updateLeaveBtn();
    renderTyping();
    renderMusic();
    if (musicShown()) { createPlayer(); reconcile(); } else pauseLocal(); // createPlayer: only if consent and a video
  }

  function teardown(room) {
    clearTimeout(room.retryTimer);
    room.retryTimer = null;
    if (room.ws) { const s = room.ws; room.ws = null; s.close(); } // ws = null makes its onclose a no-op
    room.joined = false;
    clearAllTyping(room);
    removeConversations(room);
    room.list.replaceChildren();
    room.users = new Set();
    room.avatars = new Map();
    room.myName = '';
    room.lastAuthor = null;
    room.unread = 0;
    room.outage = false;
    renderTab(room);
    if (room.id === MUSIC_ROOM) resetMusic();
  }

  // — join —
  const nickInput = $('nick');
  const avatarInput = $('avatar-url');
  try {
    const saved = localStorage.getItem(AVATAR_KEY);
    if (typeof saved === 'string') avatarInput.value = saved;
  } catch { /* storage blocked: start empty */ }
  const roomsReady = loadRooms();
  roomsReady.catch(() => {}); // handled on submit; avoids an unhandled rejection
  let joining = false;
  nickInput.addEventListener('input', () => { $('join-btn').disabled = !nickInput.value.trim(); });
  avatarInput.addEventListener('input', () => {
    avatarInput.removeAttribute('aria-invalid');
    $('join-error').hidden = true;
  });
  $('join-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const nick = nickInput.value.trim();
    if (!nick || joining) return;
    const avatar = avatarInput.value.trim();
    if (!validAvatarInput(avatar)) {
      $('join-error').textContent = 'La foto tiene que ser un link https a una imagen (.gif, .png, .jpg o .webp) de Giphy, Tenor o Imgur.';
      $('join-error').hidden = false;
      avatarInput.setAttribute('aria-invalid', 'true');
      avatarInput.focus();
      return;
    }
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
    try {
      if (avatar) localStorage.setItem(AVATAR_KEY, avatar); else localStorage.removeItem(AVATAR_KEY);
    } catch { /* storage blocked: the picture just isn't remembered */ }
    enterChat(nick, avatar);
  });

  function enterChat(nick, avatar) {
    requestedNick = nick;
    requestedAvatar = avatar;
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
      '&nickname=' + encodeURIComponent(requestedNick) +
      (requestedAvatar ? '&avatar=' + encodeURIComponent(requestedAvatar) : ''));
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
    clearAllTyping(room);
    setStatus(room, 'reconnecting');
    if (!room.outage) {
      room.outage = true;
      addSystem(room, 'Conexión perdida, reconectando…');
    }
    // The peer's side of the conversation is gone with the socket; the transcript stays.
    closeConversations(room, 'Se perdió la conexión. Esta conversación terminó.');
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
    if (room.id === MUSIC_ROOM) renderMusic();
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
        room.avatars = new Map();
        if (msg.avatars && typeof msg.avatars === 'object' && !Array.isArray(msg.avatars)) {
          for (const [nick, url] of Object.entries(msg.avatars)) {
            if (room.users.has(nick) && typeof url === 'string' && isAllowedImage(url)) room.avatars.set(nick, url);
          }
        }
        if (room === active) { renderUsers(); renderProfile(); }
      }
      return;
    }
    if (msg.type === 'history') {
      if (Array.isArray(msg.messages)) renderHistory(room, msg.messages);
      return;
    }
    // music carries no from (it has by) and exists only in the player room.
    if (msg.type === 'music') {
      if (room.id === MUSIC_ROOM) onMusicFrame(msg);
      return;
    }
    // dm_error carries no from either. It goes to the conversation it names; with no
    // such conversation (e.g. an invalid frame) it goes to the pane the user is looking at.
    if (msg.type === 'dm_error') {
      if (typeof msg.to === 'string' && typeof msg.reason === 'string') showDMError(room, msg.to, msg.reason);
      return;
    }
    if (typeof msg.from !== 'string') return;
    switch (msg.type) {
      case 'welcome':
        room.myName = msg.from;
        if (typeof msg.avatar === 'string' && isAllowedImage(msg.avatar)) room.avatars.set(msg.from, msg.avatar);
        else room.avatars.delete(msg.from);
        if (room === active) {
          $('me').textContent = room.myName;
          $('profile-nick').textContent = room.myName;
          $('me').title = room.myName;
          renderUsers();
          renderProfile();
        }
        room.attempt = 0;
        if (room.id === MUSIC_ROOM) music.awaitSnapshot = true; // a snapshot, if any, comes before our own join
        setStatus(room, 'online');
        break;
      case 'chat':
        if (typeof msg.text === 'string') {
          clearTyping(room, msg.from);
          addChat(room, msg.from, msg.text, msg.ts, msg.avatar);
          if (room !== current() && msg.from !== room.myName) bumpUnread(room);
        }
        break;
      case 'dm': {
        if (typeof msg.to !== 'string' || typeof msg.text !== 'string') break;
        const own = msg.from === room.myName;
        const peer = own ? msg.to : (msg.to === room.myName ? msg.from : null);
        if (peer === null || peer === room.myName) break; // not ours: ignore
        const conv = conversations.get(convKey(room, peer)) || createConversation(room, peer);
        if (!conv.open) reopenConversation(conv);
        if (!own) clearTyping(conv, msg.from);
        addChat(conv, msg.from, msg.text, msg.ts, room.avatars.get(msg.from));
        if (conv !== current() && !own) bumpUnread(conv);
        break;
      }
      case 'typing': {
        if (msg.from === room.myName) break;
        let pane = room;
        if (msg.to !== undefined) {
          // Never creates a tab: only a real dm does.
          const conv = msg.to === room.myName ? conversations.get(convKey(room, msg.from)) : undefined;
          if (!conv || !conv.open) break;
          pane = conv;
        }
        setTyping(pane, msg.from);
        break;
      }
      case 'join':
        room.users.add(msg.from);
        // A reused nickname must not inherit the previous owner's picture.
        if (typeof msg.avatar === 'string' && isAllowedImage(msg.avatar)) room.avatars.set(msg.from, msg.avatar);
        else room.avatars.delete(msg.from);
        if (room === active) renderUsers();
        addSystem(room, msg.from + ' se unió 🎉');
        // Our own join closes the snapshot slot: no snapshot means the server has no track (it may have restarted).
        if (room.id === MUSIC_ROOM && msg.from === room.myName && music.awaitSnapshot) {
          music.awaitSnapshot = false;
          music.srv = null;
          renderMusic();
          reconcile();
        }
        break;
      case 'leave':
        room.users.delete(msg.from);
        room.avatars.delete(msg.from);
        clearTyping(room, msg.from);
        if (room === active) renderUsers();
        addSystem(room, msg.from + ' salió 👋');
        { // a no-op when there is no conversation with them, or it is already closed
          const conv = conversations.get(convKey(room, msg.from));
          if (conv) closeConversation(conv, msg.from + ' se desconectó');
        }
        break;
      // Unknown types are ignored so newer servers don't break this page.
    }
  }

  // — typing indicator —
  function setTyping(pane, nick) {
    clearTimeout(pane.typing.get(nick));
    pane.typing.set(nick, setTimeout(() => clearTyping(pane, nick), TYPING_TTL_MS));
    if (pane === current()) renderTyping();
  }

  function clearTyping(pane, nick) {
    if (!pane.typing.has(nick)) return;
    clearTimeout(pane.typing.get(nick));
    pane.typing.delete(nick);
    if (pane === current()) renderTyping();
  }

  function clearAllTyping(pane) {
    for (const id of pane.typing.values()) clearTimeout(id);
    pane.typing.clear();
    if (pane === current()) renderTyping();
  }

  function renderTyping() {
    $('typing').textContent = typingText([...current().typing.keys()]);
  }

  function typingText(names) {
    if (names.length === 0) return '';
    if (names.length === 1) return names[0] + ' está escribiendo…';
    if (names.length === 2) return names[0] + ' y ' + names[1] + ' están escribiendo…';
    return 'Varias personas están escribiendo…';
  }

  function showDMError(room, to, reason) {
    const conv = to !== '' ? conversations.get(convKey(room, to)) : undefined;
    const pane = conv || (room === active ? current() : room);
    if (reason === 'offline') {
      const notice = to + ' no está en la sala. El mensaje no se envió.';
      if (conv && conv.open) closeConversation(conv, notice); else addSystem(pane, notice);
    } else if (reason === 'self') {
      addSystem(pane, 'No podés mandarte un mensaje privado.');
    } else {
      addSystem(pane, 'No se pudo enviar el mensaje privado.');
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
          addChat(room, m.from, m.text, m.ts, m.avatar);
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
    const room = active;
    const names = [...room.users].sort((a, b) => a.localeCompare(b, 'es', { sensitivity: 'base' }));
    $('users-count').textContent = String(room.users.size);
    $('users-list').replaceChildren(...names.map((name) => {
      const li = document.createElement('li');
      const label = document.createElement('span');
      label.textContent = name;
      li.append(avatarEl(name, room.avatars.get(name)), label);
      if (name === room.myName) {
        const tag = document.createElement('span');
        tag.className = 'tag tag-accent';
        tag.textContent = 'vos';
        li.append(tag);
      } else {
        const dm = document.createElement('button');
        dm.type = 'button';
        dm.className = 'btn btn-ghost users-dm';
        dm.textContent = 'Mensaje privado';
        dm.setAttribute('aria-label', 'Mensaje privado a ' + name);
        dm.addEventListener('click', () => openConversation(room, name));
        li.append(dm);
      }
      return li;
    }));
  }

  // addSystem, addChat and append take a pane: a room or a conversation (both have list and lastAuthor).
  function addSystem(pane, text) {
    pane.lastAuthor = null;
    const li = document.createElement('li');
    li.className = 'msg-system';
    li.textContent = text;
    append(pane, li);
  }

  function addChat(pane, author, text, ts, avatarUrl) {
    const own = author === ownerRoom(pane).myName;
    const showHead = author !== pane.lastAuthor;
    pane.lastAuthor = author;

    const li = document.createElement('li');
    li.className = 'msg' + (own ? ' own' : '') + (showHead ? ' has-head' : '');

    const slot = document.createElement('div');
    slot.className = 'avatar-slot';
    const body = document.createElement('div');
    body.className = 'msg-body';

    if (showHead) {
      slot.append(avatarEl(author, avatarUrl));

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
      img.onload = () => { if (pane === current()) scrollToBottom(); };
      // Fall back to the plain link text if the image can't be loaded.
      img.onerror = () => { bubble.classList.remove('media'); bubble.textContent = text; };
      bubble.append(img);
    } else {
      if (EMOJI_ONLY.test(text)) bubble.classList.add('emoji-only');
      bubble.textContent = text;
    }
    body.append(bubble);

    li.append(slot, body);
    append(pane, li);
  }

  function append(pane, node) {
    pane.list.append(node);
    if (pane === current()) scrollToBottom();
  }

  function scrollToBottom() {
    const el = $('scroller');
    el.scrollTop = el.scrollHeight;
  }

  // — sending —
  const draft = $('draft');
  draft.addEventListener('input', () => { updateSendState(); notifyTyping(); });

  function updateSendState() {
    const online = !!active && active.status === 'online' && (!activeConv || activeConv.open);
    if (!activeConv) draft.placeholder = 'Escribí algo…';
    else draft.placeholder = activeConv.open ? 'Mensaje privado a ' + activeConv.peer + '…' : activeConv.peer + ' se desconectó';
    $('send-btn').disabled = !draft.value.trim() || !online;
    $('gif-send').disabled = !isAllowedImage($('gif-url').value.trim()) || !online;
  }

  // The room whose socket can carry what the composer sends now, or null.
  function composerRoom() {
    const room = active;
    if (!room || room.status !== 'online' || !room.ws || room.ws.readyState !== WebSocket.OPEN) return null;
    if (activeConv && !activeConv.open) return null;
    return room;
  }

  function notifyTyping() {
    if (draft.value.trim() === '') return;
    const room = composerRoom();
    if (!room) return;
    const pane = current();
    if (pane === typingPane && performance.now() - typingSentAt < TYPING_SEND_MS) return;
    typingPane = pane;
    typingSentAt = performance.now();
    room.ws.send(JSON.stringify(activeConv ? { type: 'typing', to: activeConv.peer } : { type: 'typing' }));
  }

  function sendText(text) {
    const room = composerRoom();
    if (!room) return false;
    // Always an envelope, so text that merely looks like a DM envelope stays room chat.
    const frame = JSON.stringify(activeConv ? { type: 'dm', to: activeConv.peer, text } : { type: 'chat', text });
    // The server limit applies to the whole encoded frame. The echo renders the message.
    if (new TextEncoder().encode(frame).length > MAX_MESSAGE_BYTES) {
      addSystem(current(), 'El mensaje es demasiado largo.');
      return false;
    }
    room.ws.send(frame);
    typingPane = null; // receivers clear us on this message, so the next edit signals again
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
    if (key === 'gif') openGifPanel();
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
        notifyTyping();
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

  // — GIF search —
  const GIF_DEBOUNCE_MS = 300;
  const gifSearch = $('gif-search'), gifGrid = $('gif-grid'), gifStatus = $('gif-status');
  let gifSeq = 0; // id of the latest /gifs request; older responses are ignored
  let gifTimer = 0;
  let gifLoaded = false; // a result list has rendered at least once
  let gifPending = false; // the latest /gifs request is still in flight
  let gifSearchOff = false; // /gifs answered 404 (no key): only the paste box is left

  function openGifPanel() {
    if (gifSearchOff) { gifUrl.focus(); return; }
    gifSearch.focus();
    if (!gifLoaded && !gifPending) loadGifs(gifSearch.value.trim());
  }

  async function loadGifs(q) {
    const seq = ++gifSeq;
    gifPending = true;
    gifStatus.textContent = 'Cargando…';
    try {
      const res = await fetch('/gifs?q=' + encodeURIComponent(q));
      if (seq !== gifSeq) return;
      if (res.status === 404) { disableGifSearch(); return; }
      if (!res.ok) throw new Error('gifs: ' + res.status);
      const body = await res.json();
      if (seq !== gifSeq) return;
      if (!body || !Array.isArray(body.gifs)) throw new Error('gifs: bad body');
      renderGifs(body.gifs);
    } catch {
      if (seq === gifSeq) showGifError();
    } finally {
      if (seq === gifSeq) gifPending = false;
    }
  }

  function renderGifs(list) {
    const items = [];
    for (const g of list) {
      if (!g || typeof g.url !== 'string' || !isAllowedImage(g.url)) continue;
      const preview = typeof g.preview === 'string' && isAllowedImage(g.preview) ? g.preview : g.url;
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'gif-item';
      btn.setAttribute('aria-label', 'Enviar GIF');
      const img = document.createElement('img');
      img.alt = '';
      img.loading = 'lazy';
      img.decoding = 'async';
      img.referrerPolicy = 'no-referrer';
      img.src = preview;
      btn.append(img);
      btn.addEventListener('click', () => { if (sendText(g.url)) closePanels(); });
      items.push(btn);
    }
    gifGrid.replaceChildren(...items);
    gifStatus.textContent = items.length === 0 ? 'Sin resultados' : '';
    gifLoaded = true;
  }

  function showGifError() {
    gifGrid.replaceChildren();
    gifStatus.textContent = 'No se pudieron cargar los GIF';
  }

  function disableGifSearch() {
    gifSearchOff = true;
    const hadFocus = document.activeElement === gifSearch;
    $('gif-search-box').hidden = true;
    if (hadFocus) gifUrl.focus();
  }

  gifSearch.addEventListener('input', () => {
    clearTimeout(gifTimer);
    gifTimer = setTimeout(() => loadGifs(gifSearch.value.trim()), GIF_DEBOUNCE_MS);
  });
  gifSearch.addEventListener('keydown', (e) => {
    if (e.key !== 'Enter' || e.isComposing) return;
    e.preventDefault(); // the panel is inside #send-form: Enter would submit the draft
    clearTimeout(gifTimer);
    loadGifs(gifSearch.value.trim());
  });

  // — shared music (CHAT-30, CHAT-31) —
  // Only the musica room has a player. The server holds its state; this page follows it.
  // Nothing here contacts YouTube until the user presses "Escuchar", and no YouTube script is
  // ever loaded: the iframe is driven with postMessage, the way YouTube's own widget does it.
  const MUSIC_ROOM = 'musica'; // mirrors musicRoom in cmd/chatroom/main.go
  const YT_ORIGIN = 'https://www.youtube-nocookie.com';
  const YT_EMBED = 'https://www.youtube-nocookie.com/embed/';
  const YT_ID = /^[A-Za-z0-9_-]{11}$/;
  const YT_WATCH_HOSTS = new Set(['www.youtube.com', 'youtube.com', 'm.youtube.com', 'music.youtube.com']);
  const YT_SHORTS_HOSTS = new Set(['www.youtube.com', 'youtube.com', 'm.youtube.com']);
  const YT_SHORT_LINK_PATH = /^\/([A-Za-z0-9_-]{11})$/;
  const YT_SHORTS_PATH = /^\/shorts\/([A-Za-z0-9_-]{11})\/?$/;
  const YT_STATES = new Set([-1, 0, 1, 2, 3, 5]); // playerState values
  const YT_LISTEN_MS = 250; // handshake period (the widget's own)
  const YT_LISTEN_TRIES = 40; // 10 s without an answer: give up
  const YT_MAX_MESSAGE = 65536;
  const MUSIC_DRIFT_MS = 2000;
  const MUSIC_SETTLE_MS = 3000; // after a command, give the player time before judging it
  const MUSIC_SEND_GAP_MS = 300; // stays above the server's 260 ms gate (musicInterval)
  const MUSIC_WAIT_TEXT = 'Esperá un segundo…';
  const MUSIC_TICK_MS = 500;
  // Fixed texts by error code: nothing from the iframe ever reaches the DOM.
  const MUSIC_ERRORS = {
    100: 'El video no existe o es privado.',
    101: 'Este video no se puede reproducir fuera de YouTube.',
    150: 'Este video no se puede reproducir fuera de YouTube.',
    153: 'YouTube rechazó el reproductor (falta el referer).',
  };

  const music = {
    consent: false, // "Escuchar" pressed: the iframe may exist (kept for the page's life)
    blocked: false, // the browser refused to start playback: ask for a click again
    frame: null, ready: false, listenTimer: null,
    srv: null, // {video, playing, pos, by, at}: the server's state, the only thing the UI shows
    awaitSnapshot: false, // between welcome and our own join: a snapshot may still arrive
    loaded: '', // video id handed to the player
    state: -1, time: 0, timeAt: 0, duration: 0, // what the player last reported (seconds)
    settleUntil: 0, playTries: 0,
    errored: false, // the embed reported an error for this video: no play retries, no "Escuchar" again
    seeking: false, tick: null, lastSend: -Infinity,
    pendingSeek: null, seekTimer: null, // a seek that arrived inside the send gap (the latest one wins)
    muted: false,
  };

  const musicRoom = () => rooms.get(MUSIC_ROOM);
  const musicShown = () => { const r = musicRoom(); return !!r && r.joined && active === r; };

  // The 11-character id for the link forms CHAT-31 allows, else null.
  function parseYouTubeId(text) {
    let url;
    try { url = new URL(String(text).trim()); } catch { return null; }
    if (url.protocol !== 'https:' || url.port !== '' || url.username !== '' || url.password !== '') return null;
    let id = null;
    let m;
    if (YT_WATCH_HOSTS.has(url.hostname) && url.pathname === '/watch') {
      id = url.searchParams.get('v');
    } else if (url.hostname === 'youtu.be' && (m = YT_SHORT_LINK_PATH.exec(url.pathname))) {
      id = m[1];
    } else if (YT_SHORTS_HOSTS.has(url.hostname) && (m = YT_SHORTS_PATH.exec(url.pathname))) {
      id = m[1];
    }
    return id !== null && YT_ID.test(id) ? id : null;
  }

  // Controls only: the page shows what the server answers, never what it asked for.
  // Inside the send gap a seek is held and sent when the gap ends (the latest wins, so dragging
  // the bar still lands where the user let go); anything else is refused with a short message.
  function sendMusic(action, extra) {
    const room = musicRoom();
    if (!room || room.status !== 'online' || !room.ws || room.ws.readyState !== WebSocket.OPEN) return false;
    const now = performance.now();
    const wait = music.lastSend + MUSIC_SEND_GAP_MS - now;
    if (wait > 0) {
      if (action === 'seek') {
        music.pendingSeek = extra;
        if (music.seekTimer === null) music.seekTimer = setTimeout(flushSeek, wait);
        return true;
      }
      setMusicError(MUSIC_WAIT_TEXT);
      setTimeout(() => { if ($('music-error').textContent === MUSIC_WAIT_TEXT) setMusicError(''); }, 1500);
      return false;
    }
    music.lastSend = now;
    room.ws.send(JSON.stringify({ ...extra, type: 'music', action }));
    return true;
  }

  function flushSeek() {
    const extra = music.pendingSeek;
    music.seekTimer = null;
    music.pendingSeek = null;
    if (extra) sendMusic('seek', extra); // goes through the gap check again
  }

  function onMusicFrame(msg) {
    if (typeof msg.video !== 'string' || !YT_ID.test(msg.video) || typeof msg.playing !== 'boolean' ||
        !Number.isFinite(msg.pos) || msg.pos < 0 || typeof msg.by !== 'string') return;
    music.awaitSnapshot = false;
    if (!music.srv || music.srv.video !== msg.video) music.duration = 0;
    music.srv = { video: msg.video, playing: msg.playing, pos: msg.pos, by: msg.by, at: performance.now() };
    music.settleUntil = 0; // a global action applies at once
    music.playTries = 0;
    createPlayer(); // a no-op without consent, while hidden, or with a player already
    renderMusic();
    reconcile();
  }

  // Where the shared track is now, in ms.
  function expectedMs() {
    const s = music.srv;
    return s ? s.pos + (s.playing ? performance.now() - s.at : 0) : 0;
  }

  // The player's own position, extrapolated like the widget does (at most 1 s ahead).
  function localMs() {
    return music.time * 1000 + (music.state === 1 ? Math.min(performance.now() - music.timeAt, 1000) : 0);
  }

  const musicEnded = () => !!music.srv && music.duration > 0 && expectedMs() >= music.duration * 1000;

  function formatClock(ms) {
    const total = Math.max(0, Math.floor(ms / 1000));
    const h = Math.floor(total / 3600);
    const m = Math.floor(total / 60) % 60;
    const s = String(total % 60).padStart(2, '0');
    return h > 0 ? h + ':' + String(m).padStart(2, '0') + ':' + s : m + ':' + s;
  }

  function setMusicError(text) {
    $('music-error').textContent = text;
    $('music-error').hidden = !text;
  }

  function showMusicError(code) {
    setMusicError(MUSIC_ERRORS[code] || 'No se pudo reproducir el video.');
  }

  function renderMusic() {
    const shown = musicShown();
    $('music').hidden = !shown;
    if (!shown) {
      clearInterval(music.tick);
      music.tick = null;
      return;
    }
    if (music.tick === null) music.tick = setInterval(musicTick, MUSIC_TICK_MS);
    const room = musicRoom();
    const online = room.status === 'online';
    const s = music.srv;
    const ended = musicEnded();
    $('music-load').disabled = !online;
    const toggle = $('music-toggle');
    toggle.disabled = !s || !online;
    if (ended) { toggle.textContent = '↺'; toggle.setAttribute('aria-label', 'Repetir'); }
    else if (s && s.playing) { toggle.textContent = '⏸'; toggle.setAttribute('aria-label', 'Pausar para todos'); }
    else { toggle.textContent = '▶'; toggle.setAttribute('aria-label', 'Reproducir para todos'); }

    const seek = $('music-seek');
    const dur = music.duration;
    seek.disabled = !s || !online || !(dur > 0);
    seek.max = String(dur > 0 ? Math.floor(dur) : 0);
    if (!music.seeking) seek.value = String(dur > 0 ? Math.min(Math.floor(expectedMs() / 1000), Math.floor(dur)) : 0);

    let time = '0:00';
    if (s && ended) time = 'Terminó';
    else if (s) time = formatClock(dur > 0 ? Math.min(expectedMs(), dur * 1000) : expectedMs()) + (dur > 0 ? ' / ' + formatClock(dur * 1000) : '');
    $('music-time').textContent = time;
    $('music-now').textContent = s ? 'youtu.be/' + s.video + ' · último cambio: ' + s.by : 'No suena nada. Pegá un link y tocá Poner.';
    $('music-consent').hidden = music.consent && (!music.blocked || music.errored);
    $('music-player').hidden = !music.frame;
  }

  // reconcile() here (not only on frames) retries a stalled player and heals drift, and it also
  // undoes a pause the user made inside the iframe.
  function musicTick() {
    renderMusic();
    reconcile();
  }

  // — the YouTube iframe —
  // Built only after "Escuchar", and only while the musica pane is on screen (never while the
  // user is in another room). Every attribute is set before it is appended, so the very first
  // request already carries the referrer policy.
  function createPlayer() {
    if (music.frame || !music.consent || !music.srv || !musicShown()) return;
    const frame = document.createElement('iframe');
    frame.title = 'Reproductor de YouTube';
    frame.referrerPolicy = 'strict-origin-when-cross-origin'; // keeps error 153 away; sends only the origin
    frame.allow = 'autoplay; encrypted-media';
    frame.width = '356';
    frame.height = '200';
    frame.addEventListener('load', startListening);
    frame.src = YT_EMBED + music.srv.video + '?enablejsapi=1&controls=0&disablekb=1&playsinline=1&rel=0' +
      '&iv_load_policy=3&widgetid=1&origin=' + encodeURIComponent(location.origin);
    music.frame = frame;
    $('music-player').append(frame);
    $('music-player').hidden = false;
  }

  function ytPost(payload) {
    const target = music.frame && music.frame.contentWindow;
    if (target) target.postMessage(JSON.stringify(payload), YT_ORIGIN);
  }

  // A no-op until the player answers; reconcile() re-derives every command afterwards, so there is no queue.
  function ytCommand(func, args = []) {
    if (music.ready) ytPost({ event: 'command', func, args, id: 1, channel: 'widget' });
  }

  // The player ignores us until it hears "listening"; it answers with its first message.
  function startListening() {
    if (music.ready || !music.frame) return;
    clearInterval(music.listenTimer);
    let tries = 0;
    const ping = () => {
      if (++tries > YT_LISTEN_TRIES) {
        // Dead end otherwise: drop the frame and ask for "Escuchar" again, which builds a new one.
        clearInterval(music.listenTimer);
        music.listenTimer = null;
        if (music.frame) { music.frame.remove(); music.frame = null; }
        music.ready = false;
        music.loaded = '';
        music.blocked = true;
        setMusicError('No se pudo cargar el reproductor.');
        renderMusic();
        return;
      }
      ytPost({ event: 'listening', id: 1, channel: 'widget' });
    };
    music.listenTimer = setInterval(ping, YT_LISTEN_MS);
    ping();
  }

  function onYTMessage(e) {
    const frame = music.frame;
    if (!frame || e.source !== frame.contentWindow || e.origin !== YT_ORIGIN) return;
    if (typeof e.data !== 'string' || e.data.length > YT_MAX_MESSAGE) return;
    let msg;
    try { msg = JSON.parse(e.data); } catch { return; }
    if (!msg || typeof msg !== 'object' || Array.isArray(msg) || typeof msg.event !== 'string') return;
    if (!music.ready) {
      music.ready = true;
      clearInterval(music.listenTimer);
      music.listenTimer = null;
      for (const name of ['onStateChange', 'onError', 'onAutoplayBlocked']) ytCommand('addEventListener', [name]);
      ytCommand('setVolume', [Number($('music-volume').value)]);
      ytCommand(music.muted ? 'mute' : 'unMute');
    }
    const info = msg.info;
    switch (msg.event) {
      case 'initialDelivery':
      case 'infoDelivery':
        if (info && typeof info === 'object' && !Array.isArray(info)) {
          if (Number.isFinite(info.currentTime) && info.currentTime >= 0) {
            music.time = info.currentTime;
            music.timeAt = performance.now();
          }
          if (YT_STATES.has(info.playerState)) music.state = info.playerState;
          // Ignore a duration that may still belong to the previous video.
          if (Number.isFinite(info.duration) && info.duration > 0 && music.srv && music.loaded === music.srv.video) {
            music.duration = info.duration;
          }
        }
        break;
      case 'onStateChange':
        if (YT_STATES.has(info)) music.state = info;
        break;
      case 'onError':
        music.errored = true; // retrying play cannot fix an embed error
        showMusicError(Number.isInteger(info) ? info : 0);
        break;
      case 'onAutoplayBlocked':
        if (!music.errored) music.blocked = true;
        break;
    }
    renderMusic();
    reconcile();
  }
  window.addEventListener('message', onYTMessage);

  // Makes the local player match the server state. Idempotent: safe to call from anywhere.
  // Local player events are never sent as controls; the server is the only authority.
  function reconcile() {
    if (!music.ready || !musicShown()) return;
    const s = music.srv;
    const now = performance.now();
    if (music.state === 1 && (music.playTries !== 0 || music.blocked)) {
      music.playTries = 0;
      music.blocked = false;
      renderMusic();
    }
    if (!s) {
      if (music.state === 1 || music.state === 3) ytCommand('pauseVideo');
      return;
    }
    if (s.video !== music.loaded) {
      ytCommand(s.playing ? 'loadVideoById' : 'cueVideoById', [s.video, expectedMs() / 1000]);
      music.loaded = s.video;
      music.duration = 0;
      music.time = 0;
      music.state = -1;
      music.settleUntil = now + MUSIC_SETTLE_MS;
      music.errored = false; // a new video gets a fresh chance
      setMusicError('');
      return;
    }
    const ended = musicEnded();
    if (s.playing && !ended && music.state !== 1 && music.state !== 3) {
      if (!music.blocked && !music.errored && now >= music.settleUntil) {
        if (music.playTries >= 2) {
          music.blocked = true; // two tries went nowhere: ask for a click
          renderMusic();
        } else {
          music.playTries++;
          // An ended player (state 0) would restart from 0 on playVideo, so move it to the
          // server position first.
          if (music.state === 0) ytCommand('seekTo', [expectedMs() / 1000, true]);
          ytCommand('playVideo');
          music.settleUntil = now + MUSIC_SETTLE_MS;
        }
      }
    } else if (!s.playing && (music.state === 1 || music.state === 3)) {
      ytCommand('pauseVideo');
    }
    if (!ended && music.state !== -1 && now >= music.settleUntil &&
        Math.abs(localMs() - expectedMs()) > MUSIC_DRIFT_MS) {
      ytCommand('seekTo', [expectedMs() / 1000, true]);
      if (!s.playing) ytCommand('pauseVideo'); // a cued or unstarted video may start playing on seek
      music.settleUntil = now + MUSIC_SETTLE_MS;
    }
  }

  // Leaving musica silences the local player only: no control goes to the room.
  function pauseLocal() {
    ytCommand('pauseVideo');
  }

  // The room's socket is gone for good (leave): back to the state before any frame. Consent stays.
  function resetMusic() {
    clearInterval(music.listenTimer);
    music.listenTimer = null;
    if (music.frame) { music.frame.remove(); music.frame = null; }
    music.ready = false;
    music.blocked = false;
    music.errored = false;
    clearTimeout(music.seekTimer);
    music.seekTimer = null;
    music.pendingSeek = null;
    music.srv = null;
    music.awaitSnapshot = false;
    music.loaded = '';
    music.state = -1;
    music.time = 0;
    music.timeAt = 0;
    music.duration = 0;
    music.settleUntil = 0;
    music.playTries = 0;
    music.seeking = false;
    setMusicError('');
    renderMusic();
  }

  // — strip handlers —
  $('music-form').addEventListener('submit', (e) => {
    e.preventDefault();
    const id = parseYouTubeId($('music-url').value);
    if (id === null) {
      setMusicError('Ese link no es de YouTube.');
      return;
    }
    if (sendMusic('load', { video: id })) {
      $('music-url').value = '';
      setMusicError('');
    }
  });
  $('music-url').addEventListener('input', () => setMusicError(''));

  $('music-toggle').addEventListener('click', () => {
    const s = music.srv;
    if (!s) return;
    if (musicEnded()) sendMusic('seek', { pos: 0 });
    else sendMusic(s.playing ? 'pause' : 'play');
  });

  $('music-seek').addEventListener('input', () => { music.seeking = true; });
  $('music-seek').addEventListener('change', () => {
    music.seeking = false;
    sendMusic('seek', { pos: Math.round(Number($('music-seek').value) * 1000) });
  });

  $('music-mute').addEventListener('click', () => {
    music.muted = !music.muted;
    $('music-mute').setAttribute('aria-pressed', String(music.muted));
    $('music-mute').textContent = music.muted ? '🔇' : '🔊';
    ytCommand(music.muted ? 'mute' : 'unMute');
  });
  $('music-volume').addEventListener('input', () => {
    ytCommand('setVolume', [Number($('music-volume').value)]);
  });

  // The one consent point: the first request to YouTube happens here.
  $('music-listen').addEventListener('click', () => {
    music.consent = true;
    music.blocked = false;
    music.playTries = 0;
    music.settleUntil = 0;
    if (music.srv && !music.frame) { setMusicError(''); createPlayer(); } // a retry after a failed handshake starts clean
    else if (music.srv && music.srv.playing) ytCommand('playVideo');
    renderMusic();
  });
})();

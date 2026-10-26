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
    syncShown();
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
          music.loadedSeq = 0;
          music.endedSeq = 0;
          music.finishedSeq = 0;
          music.duration = 0;
          renderQueue();
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
  // Nothing here contacts YouTube until the musica pane is shown while a video is current (other
  // rooms never do), and no YouTube script is ever loaded: the iframe is driven with postMessage,
  // the way YouTube's own widget does it. "Escuchar" only appears when the browser blocks audio.
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
  const MUSIC_ASSERT_MS = 2000; // while the player has never reported a state: re-send the server's intent this often
  const MUSIC_STALE_MS = 1000; // after a load: ignore the widget's reports of the previous video this long
  const MUSIC_SEEK_FRAME_MS = 1000; // a frame this far from where we expected the track is a server seek
  const MUSIC_QUEUE_MAX = 30; // mirrors maxMusicQueue in internal/chat/music.go
  const MUSIC_END_GRACE_MS = 2000; // time-based end report: this long past the reported duration
  const MUSIC_PLATEAU_MS = 2000; // the player's clock stood still this long while the room plays: the track ended
  const MUSIC_PLATEAU_NEAR_END_MS = 3000; // with a known duration, a plateau only counts this close to the end
  const MUSIC_END_RETRY_MS = 3000; // the server dropped an `ended` (gate): resend while the room still plays the same seq
  // Fixed texts by error code: nothing from the iframe ever reaches the DOM.
  const MUSIC_ERRORS = {
    100: 'El video no existe o es privado.',
    101: 'Este video no se puede reproducir fuera de YouTube.',
    150: 'Este video no se puede reproducir fuera de YouTube.',
    153: 'YouTube rechazó el reproductor (falta el referer).',
  };

  const music = {
    shown: false, // the pane was visible at the last activate (see syncShown)
    blocked: false, // the browser refused to start playback: ask for a click again
    frame: null, ready: false, listenTimer: null,
    srv: null, // {video, playing, pos, by, seq, queue, at}: the server's state, the only thing the UI shows
    awaitSnapshot: false, // between welcome and our own join: a snapshot may still arrive
    loadedSeq: 0, // seq of the track handed to the player (0 = none); ids repeat, seq does not
    endedSeq: 0, // seq whose end this page already reported
    endedAt: 0, // when that report was sent
    finishedSeq: 0, // seq that ended locally: never auto-replayed until a frame moves on (seek, resume, new seq)
    timeMovedAt: 0, // last time the player's currentTime actually changed
    staleEndUntil: 0, // until then a reported state 0 may belong to what the page just replaced
    state: -1, time: 0, timeAt: 0, duration: 0, // what the player last reported (seconds)
    reported: false, // the player has reported a playerState for the loaded video (the real widget may never)
    assertAt: 0, // last blind play/pause while the state is unknown
    settleUntil: 0, playTries: 0, ignoreInfoUntil: 0,
    errored: false, // the embed reported an error for this video: no play retries, no "Escuchar" again
    seeking: false, tick: null, lastSend: -Infinity,
    pendingSeek: null, seekTimer: null, // a seek that arrived inside the send gap (the latest one wins)
    muted: false,
    title: '', // widget-confirmed title for music.srv.video; reset on track change
  };

  const musicRoom = () => rooms.get(MUSIC_ROOM);
  const musicShown = () => { const r = musicRoom(); return !!r && r.joined && active === r && !activeConv; };

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

  // A fresh copy of the queue when every item is well formed and there are at most
  // MUSIC_QUEUE_MAX of them, else null (the whole frame is then dropped).
  function cleanQueue(queue) {
    if (!Array.isArray(queue) || queue.length > MUSIC_QUEUE_MAX) return null;
    const items = [];
    for (const it of queue) {
      if (!it || typeof it !== 'object' || Array.isArray(it) || !Number.isInteger(it.id) || it.id < 1 ||
          typeof it.video !== 'string' || !YT_ID.test(it.video) || typeof it.by !== 'string') return null;
      items.push({ id: it.id, video: it.video, by: it.by });
    }
    return items;
  }

  function onMusicFrame(msg) {
    if (typeof msg.video !== 'string' || !YT_ID.test(msg.video) || typeof msg.playing !== 'boolean' ||
        !Number.isFinite(msg.pos) || msg.pos < 0 || typeof msg.by !== 'string' ||
        !Number.isInteger(msg.seq) || msg.seq < 1 || !Number.isFinite(msg.ts)) return;
    const queue = cleanQueue(msg.queue);
    if (queue === null) return;
    music.awaitSnapshot = false;
    const prev = music.srv;
    const sameTrack = !!prev && prev.seq === msg.seq && prev.video === msg.video;
    if (!sameTrack) {
      // seq going backwards (or repeating with another video) means the server restarted: forget the old identity.
      if (prev && msg.seq <= prev.seq) {
        music.loadedSeq = 0;
        music.endedSeq = 0;
        music.finishedSeq = 0;
      }
      music.duration = 0;
      music.title = '';
      // A seek held for the previous track must not land on the new one.
      clearTimeout(music.seekTimer);
      music.seekTimer = null;
      music.pendingSeek = null;
    } else if (!prev.playing && msg.playing) {
      music.endedSeq = 0; // any paused -> playing transition of the same seq re-arms the ended report
      music.finishedSeq = 0;
      music.timeMovedAt = performance.now();
    }
    // A frame far from where the track should be now is a server seek (or a new track).
    const seek = sameTrack && Math.abs(msg.pos - expectedMs()) > MUSIC_SEEK_FRAME_MS;
    if (seek) {
      music.finishedSeq = 0; // the room moved inside the track: it is playable again
      music.endedSeq = 0;
    }
    music.srv = { video: msg.video, playing: msg.playing, pos: msg.pos, by: msg.by, seq: msg.seq, queue, at: performance.now() };
    music.settleUntil = 0; // a global action applies at once
    music.playTries = 0;
    createPlayer(); // a no-op without a video, while hidden, or with a player already
    renderQueue();
    renderMusic();
    reconcile({ seek });
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

  // The start argument for load/cue: whole seconds, finite and >= 0 (a bare float such as 1e-7
  // is how a just-received frame would otherwise be serialised).
  const startSeconds = () => Math.floor(Math.max(0, expectedMs()) / 1000) || 0;

  // seekTo target: never past the end of a known duration (a seek beyond it can derail the embed).
  function seekSeconds() {
    const t = Math.max(0, expectedMs() / 1000) || 0;
    return music.duration > 0 ? Math.min(t, music.duration) : t;
  }

  const musicEnded = () => !!music.srv && music.duration > 0 && expectedMs() >= music.duration * 1000;

  // The track is over for this page: by the clock, or latched by checkLocalEnd (state 0, plateau).
  // The latch does not need a known duration, and is what keeps every play re-send away from a finished track.
  const trackDone = () => musicEnded() || (!!music.srv && music.finishedSeq === music.srv.seq);

  // Latches the end of the loaded track. Three signals, any of which is enough: the widget's state 0,
  // the room clock past a known duration, or the widget's clock standing still while the room plays
  // (a silent widget, or one whose duration never arrived: infoDelivery only carries changes).
  function checkLocalEnd() {
    const s = music.srv;
    if (!s || music.loadedSeq !== s.seq || music.finishedSeq === s.seq) return;
    const dur = music.duration;
    const clockEnd = s.playing && dur > 0 && expectedMs() >= dur * 1000 + MUSIC_END_GRACE_MS;
    const stalled = s.playing && music.time > 0 && !music.blocked && !music.errored &&
      (music.state === -1 || music.state === 0 || music.state === 1) &&
      (dur <= 0 || music.time * 1000 >= dur * 1000 - MUSIC_PLATEAU_NEAR_END_MS) &&
      performance.now() - music.timeMovedAt >= MUSIC_PLATEAU_MS;
    if (music.state === 0 || clockEnd || stalled) music.finishedSeq = s.seq;
  }

  // The seekTo every path uses. The player's own time is updated at once, so a stale pre-seek
  // currentTime cannot trigger a second, wrong drift seek; a state 0 left over from the track
  // that just ended is forgotten (the seek replaces it).
  function seekLocal() {
    const now = performance.now();
    const t = seekSeconds();
    ytCommand('seekTo', [t, true]);
    music.time = t;
    music.timeAt = now;
    music.timeMovedAt = now;
    music.staleEndUntil = now + MUSIC_STALE_MS;
    if (music.state === 0) music.state = -1;
  }

  // Frames only (never the tick), so a remove button keeps focus. Texts reach the DOM via textContent.
  function renderQueue() {
    const list = $('music-queue');
    const queue = music.srv ? music.srv.queue : [];
    list.replaceChildren(...queue.map((item) => {
      const li = document.createElement('li');
      li.className = 'music-queue-item';
      const label = document.createElement('span');
      label.textContent = 'youtu.be/' + item.video + ' · agregado por ' + item.by;
      const rm = document.createElement('button');
      rm.type = 'button';
      rm.className = 'btn btn-ghost';
      rm.textContent = '✕';
      rm.setAttribute('aria-label', 'Quitar de la cola');
      rm.dataset.id = String(item.id);
      li.append(label, rm);
      return li;
    }));
    list.hidden = queue.length === 0;
  }

  // Reports the end of the loaded track once per play-through; the server dedupes across clients.
  // See checkLocalEnd for what counts as an end: the real widget may never report a state or a
  // duration, and the queue must not stall. An `ended` the server dropped is resent every few seconds.
  function maybeReportEnded() {
    const s = music.srv;
    if (!s || music.loadedSeq !== s.seq) return;
    checkLocalEnd();
    if (music.finishedSeq !== s.seq) return;
    const now = performance.now();
    if (music.endedSeq === s.seq && !(s.playing && now - music.endedAt >= MUSIC_END_RETRY_MS)) return;
    if (sendEnded(s.seq)) { music.endedSeq = s.seq; music.endedAt = now; }
  }

  // Not sendMusic: the server gates `ended` apart from the controls, so the 300 ms gap does not apply.
  function sendEnded(seq) {
    const room = musicRoom();
    if (!room || room.status !== 'online' || !room.ws || room.ws.readyState !== WebSocket.OPEN) return false;
    room.ws.send(JSON.stringify({ type: 'music', action: 'ended', seq }));
    return true;
  }

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
    const ended = trackDone();
    $('music-load').disabled = !online;
    const toggle = $('music-toggle');
    toggle.disabled = !s || !online;
    const skip = $('music-skip');
    skip.disabled = !s || !online || s.queue.length === 0;
    for (const b of $('music-queue').querySelectorAll('button')) b.disabled = !online;
    if (ended && s && !s.playing) { toggle.textContent = '↺'; toggle.setAttribute('aria-label', 'Repetir'); }
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
    const titleEl = $('music-title');
    if (music.frame && music.title) { titleEl.textContent = music.title; titleEl.hidden = false; }
    else { titleEl.textContent = ''; titleEl.hidden = true; }
    $('music-consent').hidden = !music.blocked || music.errored;
    $('music-player').hidden = !music.frame;
  }

  // reconcile() here (not only on frames) retries a stalled player and heals drift, and it also
  // undoes a pause the user made inside the iframe.
  function musicTick() {
    renderMusic();
    reconcile();
    maybeReportEnded();
  }

  // — the YouTube iframe —
  // Built only while the musica pane is on screen with a video current (never while the
  // user is in another room). Every attribute is set before it is appended, so the very first
  // request already carries the referrer policy.
  function createPlayer() {
    if (music.frame || !music.srv || !musicShown()) return;
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
        music.loadedSeq = 0;
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

  function setYTState(v) {
    if (!YT_STATES.has(v)) return;
    // A 0 shortly after a load/cue/seek we posted is the previous track's end, not this one's.
    if (v === 0 && performance.now() < music.staleEndUntil) return;
    music.state = v;
    music.reported = true;
  }

  function onYTMessage(e) {
    const frame = music.frame;
    if (!frame || e.source !== frame.contentWindow || e.origin !== YT_ORIGIN) return;
    if (typeof e.data !== 'string' || e.data.length > YT_MAX_MESSAGE) return;
    let msg;
    try { msg = JSON.parse(e.data); } catch { return; }
    if (!msg || typeof msg !== 'object' || Array.isArray(msg) || typeof msg.event !== 'string') return;
    // readyToListen means the widget can now receive our listening pings; it is NOT a
    // delivery. Reply with another listening and keep the handshake loop going;
    // do not mark ready here — a real delivery (initialDelivery, infoDelivery,
    // onStateChange …) does that so we can register event listeners and sync state.
    if (msg.event === 'readyToListen') {
      ytPost({ event: 'listening', id: 1, channel: 'widget' });
      return;
    }
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
          // Right after a load the widget still reports the previous video (its time, state and
          // duration) for a moment; judging drift against that would seek the new video.
          // Exception: when the widget identifies the video via videoData.video_id we can safely
          // accept duration and title early. Without duration, seekSeconds() is uncapped and a
          // drift-seek past the actual end triggers an immediate state 0 that staleEndUntil then
          // swallows, leaving the end unlatched so the next server frame restarts the video.
          const vd = info.videoData;
          // video_id match alone is sufficient; loadedSeq is not checked because the iframe
          // is created with src already pointing at srv.video, so initialDelivery (the only
          // message that carries videoData/duration in full) arrives before loadedSeq is set.
          if (typeof vd === 'object' && vd && music.srv && vd.video_id === music.srv.video) {
            if (Number.isFinite(info.duration) && info.duration > 0) music.duration = info.duration;
            if (typeof vd.title === 'string') {
              const t = vd.title.trim().slice(0, 200);
              if (t) music.title = t;
            }
          }
          if (performance.now() < music.ignoreInfoUntil) break;
          if (Number.isFinite(info.currentTime) && info.currentTime >= 0) {
            if (Math.abs(info.currentTime - music.time) > 0.05) music.timeMovedAt = performance.now();
            music.time = info.currentTime;
            music.timeAt = performance.now();
          }
          setYTState(info.playerState);
          // Ignore a duration that may still belong to the previous video.
          if (Number.isFinite(info.duration) && info.duration > 0 && music.srv && music.loadedSeq === music.srv.seq) {
            music.duration = info.duration;
          }
        }
        break;
      case 'onStateChange':
        // The widget has been seen sending a bare number and an {playerState} object.
        setYTState(info && typeof info === 'object' && !Array.isArray(info) ? info.playerState : info);
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
    maybeReportEnded();
  }
  window.addEventListener('message', onYTMessage);

  // Makes the local player match the server state. Idempotent: safe to call from anywhere.
  // Local player events are never sent as controls; the server is the only authority.
  // The real widget may not report playerState (or report it late), so decisions that depend on
  // it are not enough: a server frame (`frame` given) always posts the matching command, and while
  // no state has been reported the tick keeps re-sending the intent. Commands are idempotent.
  function reconcile(frame) {
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
    if (s.seq !== music.loadedSeq) {
      ytCommand(s.playing ? 'loadVideoById' : 'cueVideoById', [s.video, startSeconds()]);
      music.loadedSeq = s.seq;
      music.ignoreInfoUntil = now + MUSIC_STALE_MS;
      music.staleEndUntil = now + MUSIC_STALE_MS;
      music.duration = 0;
      music.time = 0;
      music.timeMovedAt = now;
      music.state = -1;
      music.reported = false;
      music.assertAt = now;
      music.settleUntil = now + MUSIC_SETTLE_MS;
      music.errored = false; // a new video gets a fresh chance
      setMusicError('');
      return;
    }
    checkLocalEnd();
    const ended = trackDone();
    if (frame) {
      if (frame.seek) seekLocal();
      if (!s.playing) ytCommand('pauseVideo');
      else if (!ended && !music.errored && (frame.seek || music.state !== 0)) ytCommand('playVideo'); // an ended player waits for the next frame

      music.assertAt = now;
      music.settleUntil = now + MUSIC_SETTLE_MS;
      return;
    }
    if (!music.reported) {
      if (now >= music.settleUntil && now >= music.assertAt + MUSIC_ASSERT_MS) {
        music.assertAt = now;
        if (!s.playing) ytCommand('pauseVideo');
        // a clock that moved lately means it is already playing: re-sending play could only restart a finished track
        else if (!ended && !music.blocked && !music.errored && !(music.time > 0 && now - music.timeMovedAt < 3 * MUSIC_PLATEAU_MS)) ytCommand('playVideo');
      }
      return;
    }
    if (s.playing && !ended && music.state !== 0 && music.state !== 1 && music.state !== 3) {
      if (!music.blocked && !music.errored && now >= music.settleUntil) {
        if (music.playTries >= 2) {
          music.blocked = true; // two tries went nowhere: ask for a click
          renderMusic();
        } else {
          music.playTries++;
          ytCommand('playVideo');
          music.settleUntil = now + MUSIC_SETTLE_MS;
        }
      }
    } else if (!s.playing && (music.state === 1 || music.state === 3)) {
      ytCommand('pauseVideo');
    }
    if (!ended && music.state !== -1 && music.state !== 0 && now >= music.settleUntil &&
        Math.abs(localMs() - expectedMs()) > MUSIC_DRIFT_MS) {
      seekLocal();
      if (!s.playing) ytCommand('pauseVideo'); // a cued or unstarted video may start playing on seek
      music.settleUntil = now + MUSIC_SETTLE_MS;
    }
  }

  // Leaving musica silences the local player only: no control goes to the room.
  function pauseLocal() {
    ytCommand('pauseVideo');
  }

  // Called by activate. On hidden -> shown the player is forced to the room's position: the
  // local one froze at the pause, and no frame says the room moved on. reconcile({seek:true}) seeks
  // then plays/pauses whatever was reported, or loads when the track changed meanwhile.
  function syncShown() {
    const was = music.shown;
    music.shown = musicShown();
    if (!music.shown) { if (was) pauseLocal(); return; }
    createPlayer(); // a no-op without a video or with a frame already
    if (was) { reconcile(); return; } // still shown (e.g. a repeated activate): no audible seek
    music.settleUntil = 0;
    music.playTries = 0;
    reconcile({ seek: true });
  }

  // The room's socket is gone for good (leave): back to the state before any frame.
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
    music.loadedSeq = 0;
    music.endedSeq = 0;
    music.endedAt = 0;
    music.finishedSeq = 0;
    music.timeMovedAt = 0;
    music.staleEndUntil = 0;
    music.shown = false;
    music.state = -1;
    music.reported = false;
    music.assertAt = 0;
    music.time = 0;
    music.timeAt = 0;
    music.duration = 0;
    music.settleUntil = 0;
    music.playTries = 0;
    music.ignoreInfoUntil = 0;
    music.seeking = false;
    music.title = '';
    setMusicError('');
    renderQueue();
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
    if (music.srv && music.srv.queue.length >= MUSIC_QUEUE_MAX) {
      setMusicError('La cola está llena');
      return;
    }
    if (sendMusic('add', { video: id })) {
      $('music-url').value = '';
      setMusicError('');
    }
  });
  $('music-url').addEventListener('input', () => setMusicError(''));

  $('music-toggle').addEventListener('click', () => {
    const s = music.srv;
    if (!s) return;
    if (trackDone() && !s.playing) {
      // play on a finished track would resume at its end, so rewind first
      if (sendMusic('seek', { pos: 0 })) setTimeout(() => sendMusic('play'), MUSIC_SEND_GAP_MS + 20);
    } else {
      sendMusic(s.playing ? 'pause' : 'play');
    }
  });

  $('music-skip').addEventListener('click', () => { sendMusic('skip'); });

  $('music-queue').addEventListener('click', (e) => {
    const b = e.target instanceof Element ? e.target.closest('button[data-id]') : null;
    if (b) sendMusic('remove', { id: Number(b.dataset.id) });
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

  // The fallback when the browser blocked audio: the click is the user activation that lets play
  // start, and it re-syncs to the room first (same forced path as returning to the pane).
  $('music-listen').addEventListener('click', () => {
    music.blocked = false;
    music.playTries = 0;
    music.settleUntil = 0;
    if (music.srv && !music.frame) { setMusicError(''); createPlayer(); } // a retry after a failed handshake starts clean
    else if (music.srv) reconcile({ seek: true });
    renderMusic();
  });
})();

(() => {
  const $ = (id) => document.getElementById(id);
  const MAX_MESSAGE_BYTES = 4096; // defaultMaxMessageSize in internal/chat/room.go

  const CATS = [
    { name: 'Caras', icon: '😀', list: '😀 😃 😄 😁 😆 😅 😂 🤣 🙂 😉 😊 😇 🥰 😍 🤩 😘 😋 😛 😜 🤪 🤔 🤨 😐 😑 😶 🙄 😏 😴 😮 😲 😢 😭 😤 😡 🥳 😎 🤓 🥺 😱 🤯 😬 🫠 🤗 🫡' },
    { name: 'Gestos', icon: '👍', list: '👍 👎 👌 ✌️ 🤞 🤟 🤘 🤙 👈 👉 👆 👇 👋 🤚 ✋ 🖖 👏 🙌 🫶 🙏 🤝 💪 ✍️ 🫰 🫵 👀 🧠 💀 👻 🤖' },
    { name: 'Corazones y símbolos', icon: '❤️', list: '❤️ 🧡 💛 💚 💙 💜 🖤 🤍 💔 💕 💖 💯 ✨ ⭐ 🔥 💥 ⚡ 🎉 🎊 ✅ ❌ ❓ ❗ 💡 💬 🔔' },
    { name: 'Cosas', icon: '🍕', list: '🍕 🍔 🍟 🌮 🍣 🍩 🍪 🍫 ☕ 🍺 🍷 🎂 ⚽ 🏀 🎮 🎧 🎸 🚀 🌈 🌙 ☀️ 🌧️ 🐶 🐱 🦊 🐼 🐸 🦄' },
  ];
  const STATUS_LABEL = { connecting: 'Conectando…', online: 'En línea', offline: 'Desconectado' };
  // Only https images are rendered inline; anything else stays as plain text.
  const IMAGE_URL = /^https:\/\/[^\s"'<>]+\.(gif|webp|png|jpe?g)(\?[^\s"'<>]*)?$/i;
  const EMOJI_ONLY = /^(\p{Extended_Pictographic}|️|‍|\s){1,12}$/u;

  let ws = null;
  let myName = ''; // identity assigned by the server in the welcome frame
  let status = 'connecting';
  let lastAuthor = null;

  // — join —
  const nickInput = $('nick');
  nickInput.addEventListener('input', () => { $('join-btn').disabled = !nickInput.value.trim(); });
  $('join-form').addEventListener('submit', (e) => {
    e.preventDefault();
    const nick = nickInput.value.trim();
    if (nick) join(nick);
  });

  function join(nick) {
    myName = '';
    lastAuthor = null;
    $('messages').replaceChildren();
    $('me').textContent = nick;
    $('me').title = nick;
    $('join-view').hidden = true;
    $('chat-view').hidden = false;
    setStatus('connecting');
    $('draft').focus();

    const scheme = location.protocol === 'https:' ? 'wss://' : 'ws://';
    const socket = new WebSocket(scheme + location.host + '/ws?nickname=' + encodeURIComponent(nick));
    ws = socket;
    socket.onopen = () => setStatus('online');
    socket.onmessage = (e) => {
      if (ws !== socket) return; // late frame from a socket the user already left
      addRaw(e.data);
    };
    socket.onclose = () => {
      if (ws !== socket) return; // the user left on purpose
      setStatus('offline');
      addSystem('Desconectado. No se pudo mantener la conexión con el servidor.');
    };
  }

  $('leave-btn').addEventListener('click', () => {
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
    if (!msg || typeof msg.from !== 'string') return;
    switch (msg.type) {
      case 'welcome':
        myName = msg.from;
        $('me').textContent = myName;
        $('me').title = myName;
        break;
      case 'chat':
        if (typeof msg.text === 'string') addChat(msg.from, msg.text, msg.ts);
        break;
      case 'join':
        addSystem(msg.from + ' se unió 🎉');
        break;
      case 'leave':
        addSystem(msg.from + ' salió 👋');
        break;
      // Unknown types are ignored so newer servers don't break this page.
    }
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
    if (IMAGE_URL.test(text)) {
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
  }

  function sendText(text) {
    if (!ws || ws.readyState !== WebSocket.OPEN) return false;
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
  gifUrl.addEventListener('input', () => { $('gif-send').disabled = !IMAGE_URL.test(gifUrl.value.trim()); });
  gifUrl.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); $('gif-send').click(); } });
  $('gif-send').addEventListener('click', () => {
    const url = gifUrl.value.trim();
    if (IMAGE_URL.test(url) && sendText(url)) {
      gifUrl.value = '';
      $('gif-send').disabled = true;
      closePanels();
    }
  });
})();

// ── State ──
let ws = null;
let myUsername = '';
window.myUsername = '';
let currentView = { type: 'room', name: 'general' };
let rooms = [];
let users = [];
let roomUsers = {};
let messageStore = {};
let unreadCounts = {};
let typingTimers = {};
let pendingImage = null; // { file, previewURL }
let reconnectAttempts = 0;
let viewRestored = false;
let tabFocused = true;
let totalUnread = 0;
const BASE_TITLE = 'WiFi Chat';
let notificationsEnabled = false;
let disappearingTimer = 0; // ms, 0 = disabled (Briar-inspired)

// Avatar color palette
const AVATAR_COLORS = ['#6c5ce7','#e17055','#00b894','#fdcb6e','#0984e3','#e84393','#00cec9','#d63031','#6c5ce7','#55a3f0'];
function getUserColor(name) {
    let h = 0;
    for (let i = 0; i < name.length; i++) h = name.charCodeAt(i) + ((h << 5) - h);
    return AVATAR_COLORS[Math.abs(h) % AVATAR_COLORS.length];
}
function getUserInitial(name) { return name ? name.charAt(0).toUpperCase() : '?'; }

// ── DOM ──
let loginScreen, chatScreen, loginForm, usernameInput, loginError, messagesDiv, messageForm, messageInput;
let chatTitle, chatSubtitle, roomListEl, dmListEl, userListEl, onlineCount, myUsernameEl, typingIndicator;
let createRoomBtn, createRoomModal, createRoomForm, roomNameInput, cancelRoomBtn, leaveRoomBtn;
let sidebar, sidebarOpen, sidebarClose, messagesContainer, logoutBtn;
let imageUploadBtn, imageFileInput, imagePreviewBar, imagePreviewThumb, imagePreviewName, imagePreviewCancel;
let emojiBtn, emojiPicker, imageLightbox, lightboxImg, lightboxClose;

// ── Emoji Data ──
const EMOJI_CATEGORIES = {
    'Smileys': ['😀','😃','😄','😁','😆','😅','🤣','😂','🙂','😊','😇','🥰','😍','🤩','😘','😗','😚','😙','🥲','😋','😛','😜','🤪','😝','🤑','🤗','🤭','🫢','🤫','🤔','🫡','🤐','🤨','😐','😑','😶','🫥','😏','😒','🙄','😬','🤥','😌','😔','😪','🤤','😴','😷','🤒','🤕','🤢','🤮','🥵','🥶','🥴','😵','🤯','🤠','🥳','🥸','😎','🤓','🧐','😕','🫤','😟','🙁','😮','😯','😲','😳','🥺','🥹','😦','😧','😨','😰','😥','😢','😭','😱','😖','😣','😞','😓','😩','😫','🥱','😤','😡','😠','🤬','😈','👿','💀','☠️','💩','🤡','👹','👺','👻','👽','👾','🤖'],
    'Gestures': ['👋','🤚','🖐️','✋','🖖','🫱','🫲','🫳','🫴','👌','🤌','🤏','✌️','🤞','🫰','🤟','🤘','🤙','👈','👉','👆','🖕','👇','☝️','🫵','👍','👎','✊','👊','🤛','🤜','👏','🙌','🫶','👐','🤲','🤝','🙏'],
    'Hearts': ['❤️','🧡','💛','💚','💙','💜','🖤','🤍','🤎','💔','❤️‍🔥','❤️‍🩹','💕','💞','💓','💗','💖','💘','💝','💟'],
    'Objects': ['🎉','🎊','🎈','🎁','🏆','🥇','⭐','🌟','💫','✨','🔥','💯','🎯','🚀','💡','📌','📎','✏️','📝','💻','📱','⌨️','🖥️','📷','🎵','🎶','🎸','🎮','🎲','♟️','🧩'],
    'Food': ['🍕','🍔','🍟','🌭','🍿','🧂','🥚','🍳','🧇','🥞','🧈','🍞','🥐','🥨','🧀','🥗','🥙','🌮','🌯','🫔','🍱','🍣','🍜','🍝','🍛','🍲','🫕','🥘','🍰','🎂','🧁','🍩','🍪','🍫','☕','🍵','🧃','🥤','🍺','🍻','🥂','🍷'],
    'Nature': ['🌸','🌹','🌺','🌻','🌼','🌷','🪷','🌱','🌲','🌳','🌴','🌵','🍀','🍁','🍂','🍃','🪻','🐶','🐱','🐭','🐰','🦊','🐻','🐼','🐨','🐯','🦁','🐸','🐵','🐔','🐧','🐦','🦅','🦋','🐛','🐝','🐞']
};

// ── WebSocket ──
async function connect(username) {
    console.log('[DEBUG] connect() called for:', username);
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${location.host}/ws`;
    console.log('[DEBUG] Creating WebSocket to:', wsUrl);
    ws = new WebSocket(wsUrl);

    ws.onopen = async () => {
        console.log('[DEBUG] WebSocket OPEN');
        reconnectAttempts = 0;
        let pubKey = '';
        try {
            pubKey = await cryptoManager.init();
            console.log('[DEBUG] Crypto init done, pubKey length:', pubKey.length);
        } catch (e) { console.warn('[E2EE] Crypto init failed', e); }
        const joinMsg = { type: 'join', username, public_key: pubKey };
        console.log('[DEBUG] Sending join message');
        ws.send(JSON.stringify(joinMsg));
        console.log('[DEBUG] Join message sent');
    };

    ws.onmessage = (event) => {
        const msg = JSON.parse(event.data);
        console.log('[DEBUG] Received message type:', msg.type);
        handleMessage(msg);
    };

    ws.onclose = (event) => {
        console.log('[DEBUG] WebSocket CLOSED, code:', event.code, 'reason:', event.reason);
        if (myUsername && reconnectAttempts < 5) {
            reconnectAttempts++;
            const delay = Math.min(1000 * Math.pow(2, reconnectAttempts), 10000);
            addSystemMessage(`Connection lost. Reconnecting in ${delay/1000}s...`);
            setTimeout(() => connect(myUsername), delay);
        } else if (reconnectAttempts >= 5) {
            showLoginError('Connection lost. Please refresh the page.');
            loginScreen.classList.remove('hidden');
            chatScreen.classList.add('hidden');
        }
    };

    ws.onerror = (err) => {
        console.error('[DEBUG] WebSocket ERROR:', err);
    };
}

function send(obj) {
    if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify(obj));
    }
}

// Generate a unique message ID for delivery tracking
function generateMsgId() {
    if (crypto.randomUUID) return crypto.randomUUID();
    return Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 10);
}

// ── Message Handler ──
function handleMessage(msg) {
    switch (msg.type) {
        case 'error':
            console.warn('[ERROR]', msg.error || msg.message);
            if (chatScreen.classList.contains('hidden')) {
                // Still on login screen — show the error there
                showLoginError(msg.error || msg.message || 'Unknown error');
            } else {
                addSystemMessage('Error: ' + (msg.error || msg.message || 'Unknown error'));
            }
            break;

        case 'system':
            loginScreen.classList.add('hidden');
            chatScreen.classList.remove('hidden');
            callManager.init();
            addSystemMessage(msg.content, msg.room);
            // Restore saved view after login
            if (!viewRestored) {
                viewRestored = true;
                const savedView = sessionStorage.getItem('wifi-chat-view');
                if (savedView) {
                    try {
                        const v = JSON.parse(savedView);
                        if (v.type === 'dm' && v.name) {
                            switchToDM(v.name);
                        } else if (v.type === 'room' && v.name && v.name !== 'general') {
                            switchToRoom(v.name);
                        }
                    } catch(e) {}
                }
            }
            break;

        case 'room_list':
            rooms = msg.rooms || [];
            renderRoomList();
            break;

        case 'user_list':
            users = (msg.users || []).filter(u => u !== myUsername);
            onlineCount.textContent = users.length + 1;
            // Import E2EE public keys
            if (msg.public_keys) {
                Object.entries(msg.public_keys).forEach(([u, k]) => {
                    if (u !== myUsername) cryptoManager.importPeerKey(u, k);
                });
            }
            renderUserList();
            renderDMList();
            break;

        case 'room_users':
            roomUsers[msg.room] = msg.users || [];
            if (currentView.type === 'room' && currentView.name === msg.room) {
                updateChatSubtitle();
            }
            break;

        case 'message':
            storeAndRender(msg.room, {
                type: 'message',
                from: msg.from,
                content: msg.content,
                image_url: msg.image_url,
                ts: msg.ts,
                message_id: msg.message_id,
            });
            break;

        case 'dm': {
            // Skip server echo of our own messages (already stored locally)
            if (msg.from === myUsername) {
                renderDMList();
                break;
            }
            const dmKey = `dm:${msg.from}`;
            (async () => {
                let content = msg.content;
                if (msg.encrypted) {
                    try {
                        const dec = await cryptoManager.decrypt(msg.from, msg.content);
                        if (dec) { content = dec; }
                        else { content = '🔒 [Encrypted message - key unavailable]'; }
                    } catch(e) {
                        console.warn('[E2EE] Decrypt failed', e);
                        content = '🔒 [Encrypted message - decryption failed]';
                    }
                }
                storeAndRender(dmKey, {
                    type: 'dm', from: msg.from, to: msg.to,
                    content: content, image_url: msg.image_url,
                    ts: msg.ts, encrypted: msg.encrypted,
                    message_id: msg.message_id, status: 'delivered',
                });
                // Auto-send read receipt if user is currently viewing this conversation
                if (currentView.type === 'dm' && currentView.name === msg.from && msg.message_id) {
                    send({ type: 'read_receipt', to: msg.from, ref_id: msg.message_id, status: 'read' });
                }
                renderDMList();
            })();
            break;
        }

        case 'dm_history': {
            const peer = msg.from;
            const dmKey = `dm:${peer}`;
            (async () => {
                const decrypted = [];
                for (const m of (msg.messages || [])) {
                    let content = m.content;
                    if (m.encrypted) {
                        const peerName = m.from === myUsername ? m.to : m.from;
                        try {
                            const dec = await cryptoManager.decrypt(peerName, m.content);
                            if (dec) { content = dec; }
                            else { content = '🔒 [Encrypted - key unavailable]'; }
                        } catch(e) { content = '🔒 [Encrypted - key unavailable]'; }
                    }
                    decrypted.push({
                        type: 'dm', from: m.from, to: m.to,
                        content: content, image_url: m.image_url,
                        ts: m.ts, encrypted: m.encrypted,
                        message_id: m.message_id, status: m.status || 'delivered',
                    });
                }
                // Merge with locally pending messages (status='sent') that server doesn't have yet
                const existing = messageStore[dmKey] || [];
                const serverIds = new Set(decrypted.map(m => m.message_id).filter(Boolean));
                const pendingLocal = existing.filter(m => m.message_id && m.status === 'sent' && !serverIds.has(m.message_id));
                messageStore[dmKey] = [...decrypted, ...pendingLocal];
                saveMessageStore();
                if (currentView.type === 'dm' && currentView.name === peer) {
                    renderMessages();
                }
            })();
            break;
        }

        case 'history': {
            const key = msg.room;
            messageStore[key] = (msg.messages || []).map(m => ({
                type: 'message',
                from: m.from,
                content: m.content,
                image_url: m.image_url,
                ts: m.ts,
                message_id: m.message_id,
                status: m.status || 'delivered',
            }));
            saveMessageStore();
            if (currentView.type === 'room' && currentView.name === key) {
                renderMessages();
            }
            break;
        }

        case 'user_joined':
            if (msg.public_key && msg.username !== myUsername) {
                cryptoManager.importPeerKey(msg.username, msg.public_key);
            }
            addSystemMessage(`${msg.username} joined`, msg.room);
            break;

        case 'key_exchange':
            if (msg.public_key && msg.from) {
                cryptoManager.importPeerKey(msg.from, msg.public_key);
            }
            break;

        case 'user_left':
            addSystemMessage(`${msg.username} left`, msg.room);
            break;

        case 'typing':
            showTyping(msg.username, msg.room, msg.to);
            break;

        case 'call_request':
            notifyIncomingCall(msg.from, msg.call_type);
            playRingtone();
            callManager.handleSignal(msg);
            break;
        case 'call_accept':
        case 'call_reject':
        case 'call_offer':
        case 'call_answer':
        case 'call_ice':
        case 'call_end':
            callManager.handleSignal(msg);
            break;

        case 'delivery_ack':
        case 'read_receipt': {
            // Update message status in store
            const refId = msg.ref_id;
            const newStatus = msg.status || (msg.type === 'read_receipt' ? 'read' : 'delivered');
            if (refId) {
                updateMessageStatus(refId, newStatus);
            }
            break;
        }
    }
}

// Update delivery status of a message by message_id
function updateMessageStatus(messageId, status) {
    let updated = false;
    for (const key in messageStore) {
        const msgs = messageStore[key];
        for (let i = 0; i < msgs.length; i++) {
            if (msgs[i].message_id === messageId) {
                msgs[i].status = status;
                updated = true;
                break;
            }
        }
        if (updated) break;
    }
    if (updated) {
        saveMessageStore();
        renderMessages();
    }
}

// ── Message Storage ──
function storeAndRender(key, msg) {
    if (!messageStore[key]) messageStore[key] = [];
    messageStore[key].push(msg);
    saveMessageStore();

    const currentKey = currentView.type === 'room' ? currentView.name : `dm:${currentView.name}`;
    if (key === currentKey) {
        appendMessage(msg);
        scrollToBottom();
    } else {
        unreadCounts[key] = (unreadCounts[key] || 0) + 1;
        renderRoomList();
        renderDMList();
        // Only notify/badge for messages from others, not own sent messages
        if (msg.from !== myUsername) {
            playNotificationSound();
            updateTitleBadge();
            const isDM = key.startsWith('dm:');
            const truncated = (msg.content || '').slice(0, 100);
            notifyMessage(msg.from || '', truncated, isDM, isDM ? (msg.from || '') : key);
        }
    }
}

function saveMessageStore() {
    try {
        sessionStorage.setItem('wifi-chat-messages', JSON.stringify(messageStore));
    } catch(e) {
        // sessionStorage full, clear old data
        console.warn('[STORAGE] sessionStorage full, clearing old messages');
        sessionStorage.removeItem('wifi-chat-messages');
    }
}

function loadMessageStore() {
    try {
        const saved = sessionStorage.getItem('wifi-chat-messages');
        if (saved) {
            messageStore = JSON.parse(saved);
            console.log('[STORAGE] Restored message history');
        }
    } catch(e) {
        console.warn('[STORAGE] Failed to restore messages', e);
    }
}

function addSystemMessage(content, room) {
    const key = room || (currentView.type === 'room' ? currentView.name : `dm:${currentView.name}`);
    const msg = { type: 'system', content, ts: Date.now() };
    if (!messageStore[key]) messageStore[key] = [];
    messageStore[key].push(msg);

    const currentKey = currentView.type === 'room' ? currentView.name : `dm:${currentView.name}`;
    if (key === currentKey) {
        appendMessage(msg);
        scrollToBottom();
    }
}

// ── Rendering ──
function renderMessages() {
    messagesDiv.innerHTML = '';
    const key = currentView.type === 'room' ? currentView.name : `dm:${currentView.name}`;
    const msgs = messageStore[key] || [];
    msgs.forEach(m => appendMessage(m));
    scrollToBottom();
}

function appendMessage(msg) {
    const div = document.createElement('div');

    if (msg.type === 'system') {
        div.className = 'msg msg-system';
        div.textContent = msg.content;
        messagesDiv.appendChild(div);
        return;
    }

    const isMine = msg.from === myUsername;
    div.className = `msg ${isMine ? 'msg-sent' : 'msg-received'}`;

    // Avatar (only for received)
    if (!isMine && msg.from) {
        const avatar = document.createElement('div');
        avatar.className = 'msg-avatar';
        avatar.style.backgroundColor = getUserColor(msg.from);
        avatar.textContent = getUserInitial(msg.from);
        div.appendChild(avatar);
    }

    const body = document.createElement('div');
    body.className = 'msg-body';

    // Header: author + time
    const header = document.createElement('div');
    header.className = 'msg-header';
    const author = document.createElement('span');
    author.className = 'msg-author';
    author.textContent = isMine ? 'You' : (msg.from || '');
    header.appendChild(author);
    if (msg.ts) {
        const time = document.createElement('span');
        time.className = 'msg-time';
        time.textContent = formatTime(msg.ts);
        header.appendChild(time);
    }
    if (msg.encrypted) {
        const lock = document.createElement('span');
        lock.className = 'msg-encrypted';
        lock.textContent = '🔒';
        lock.title = 'End-to-end encrypted';
        header.appendChild(lock);
    }
    body.appendChild(header);

    // Image
    if (msg.image_url) {
        const imgWrap = document.createElement('div');
        imgWrap.className = 'msg-image-wrap';
        const img = document.createElement('img');
        img.className = 'msg-image';
        img.src = msg.image_url;
        img.alt = 'Shared image';
        img.loading = 'lazy';
        img.addEventListener('click', () => openLightbox(msg.image_url));
        imgWrap.appendChild(img);
        body.appendChild(imgWrap);
    }

    // Text content (with code detection & syntax highlighting)
    if (msg.content) {
        const text = document.createElement('div');
        text.className = 'msg-text';
        if (typeof codeFormatter !== 'undefined') {
            const formatted = codeFormatter.formatMessage(msg.content);
            if (formatted.hasCode) {
                text.innerHTML = formatted.html;
            } else {
                text.textContent = msg.content;
            }
        } else {
            text.textContent = msg.content;
        }
        body.appendChild(text);
    }

    // Delivery status for own messages
    if (isMine && msg.status) {
        const statusEl = document.createElement('span');
        statusEl.className = 'msg-status';
        if (msg.status === 'read') {
            statusEl.textContent = '✓✓';
            statusEl.style.color = '#4FC3F7';
            statusEl.title = 'Read';
        } else if (msg.status === 'delivered') {
            statusEl.textContent = '✓✓';
            statusEl.style.color = '#999';
            statusEl.title = 'Delivered';
        } else {
            statusEl.textContent = '✓';
            statusEl.style.color = '#666';
            statusEl.title = 'Sent';
        }
        statusEl.style.fontSize = '11px';
        statusEl.style.marginLeft = '4px';
        body.appendChild(statusEl);
    }

    div.appendChild(body);
    messagesDiv.appendChild(div);
}

function scrollToBottom() {
    requestAnimationFrame(() => {
        messagesContainer.scrollTop = messagesContainer.scrollHeight;
    });
}

function formatTime(ts) {
    const d = new Date(ts);
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

function renderRoomList() {
    roomListEl.innerHTML = '';
    rooms.forEach(room => {
        const li = document.createElement('li');
        li.textContent = `# ${room}`;
        if (currentView.type === 'room' && currentView.name === room) {
            li.classList.add('active');
        }
        const count = unreadCounts[room];
        if (count) {
            const badge = document.createElement('span');
            badge.className = 'unread-badge';
            badge.textContent = count;
            li.appendChild(badge);
        }
        li.addEventListener('click', () => switchToRoom(room));
        roomListEl.appendChild(li);
    });
}

function renderDMList() {
    dmListEl.innerHTML = '';
    const dmUsers = new Set();
    Object.keys(messageStore).forEach(key => {
        if (key.startsWith('dm:')) dmUsers.add(key.slice(3));
    });
    users.forEach(u => dmUsers.add(u));

    dmUsers.forEach(user => {
        const li = document.createElement('li');
        const av = document.createElement('div');
        av.className = 'avatar avatar-sm';
        av.style.backgroundColor = getUserColor(user);
        av.textContent = getUserInitial(user);
        li.appendChild(av);

        const nameSpan = document.createElement('span');
        nameSpan.textContent = user;
        li.appendChild(nameSpan);

        if (users.includes(user)) {
            const dot = document.createElement('span');
            dot.className = 'status-dot online';
            dot.style.marginLeft = '-4px';
            li.insertBefore(dot, nameSpan);
        }

        if (cryptoManager.hasKeyFor(user)) {
            const lock = document.createElement('span');
            lock.textContent = '🔒';
            lock.style.fontSize = '10px';
            lock.style.opacity = '0.5';
            lock.title = 'E2E Encrypted';
            li.appendChild(lock);
        }

        if (currentView.type === 'dm' && currentView.name === user) {
            li.classList.add('active');
        }

        const dmKey = `dm:${user}`;
        const count = unreadCounts[dmKey];
        if (count) {
            const badge = document.createElement('span');
            badge.className = 'unread-badge';
            badge.textContent = count;
            li.appendChild(badge);
        }

        li.addEventListener('click', () => switchToDM(user));
        dmListEl.appendChild(li);
    });
}

function renderUserList() {
    userListEl.innerHTML = '';
    users.forEach(user => {
        const li = document.createElement('li');
        const av = document.createElement('div');
        av.className = 'avatar avatar-sm';
        av.style.backgroundColor = getUserColor(user);
        av.textContent = getUserInitial(user);
        li.appendChild(av);

        const dot = document.createElement('span');
        dot.className = 'status-dot online';
        li.appendChild(dot);

        const nameSpan = document.createElement('span');
        nameSpan.textContent = user;
        li.appendChild(nameSpan);

        li.addEventListener('click', () => switchToDM(user));
        userListEl.appendChild(li);
    });
}

function updateChatSubtitle() {
    const e2ee = document.getElementById('e2ee-indicator');
    if (currentView.type === 'room') {
        const members = roomUsers[currentView.name] || [];
        chatSubtitle.textContent = `${members.length} member${members.length !== 1 ? 's' : ''}`;
        if (e2ee) e2ee.classList.add('hidden');
    } else {
        const online = users.includes(currentView.name);
        chatSubtitle.textContent = online ? 'Online' : 'Offline';
        if (e2ee) {
            if (cryptoManager.hasKeyFor(currentView.name)) {
                e2ee.classList.remove('hidden');
            } else {
                e2ee.classList.add('hidden');
            }
        }
    }
}

// ── Navigation ──
function switchToRoom(room) {
    currentView = { type: 'room', name: room };
    sessionStorage.setItem('wifi-chat-view', JSON.stringify(currentView));
    chatTitle.textContent = `# ${room}`;
    leaveRoomBtn.style.display = room === 'general' ? 'none' : 'block';
    document.getElementById('call-buttons').style.display = 'none';
    unreadCounts[room] = 0;

    const joined = messageStore[room] !== undefined;
    if (!joined) {
        send({ type: 'join_room', room });
    }

    updateChatSubtitle();
    renderMessages();
    renderRoomList();
    renderDMList();
    closeSidebar();
    closeEmojiPicker();
    messageInput.focus();
}

function switchToDM(user) {
    currentView = { type: 'dm', name: user };
    sessionStorage.setItem('wifi-chat-view', JSON.stringify(currentView));
    chatTitle.textContent = `@ ${user}`;
    leaveRoomBtn.style.display = 'none';
    document.getElementById('call-buttons').style.display = 'flex';
    const dmKey = `dm:${user}`;
    unreadCounts[dmKey] = 0;

    // Request DM history from server if we don't have any
    if (!messageStore[dmKey] || messageStore[dmKey].length === 0) {
        send({ type: 'get_dm_history', to: user });
    }

    // Send read receipts for unread messages from this peer
    sendReadReceipts(user);

    updateChatSubtitle();
    renderMessages();
    renderRoomList();
    renderDMList();
    closeSidebar();
    closeEmojiPicker();
    messageInput.focus();
}

// Send read_receipt for unread DMs from a peer (called when opening a DM conversation)
function sendReadReceipts(peer) {
    const dmKey = `dm:${peer}`;
    const msgs = messageStore[dmKey] || [];
    for (const m of msgs) {
        if (m.from === peer && m.message_id && m.status !== 'read') {
            send({ type: 'read_receipt', to: peer, ref_id: m.message_id, status: 'read' });
            m.status = 'read';
        }
    }
    saveMessageStore();
}

// ── Typing ──
let typingTimeout = null;
function sendTyping() {
    if (currentView.type === 'room') {
        send({ type: 'typing', room: currentView.name });
    } else if (currentView.type === 'dm') {
        send({ type: 'typing', to: currentView.name });
    }
}

function showTyping(username, room, to) {
    // Check if this typing indicator is relevant to the current view
    if (currentView.type === 'room') {
        if (room !== currentView.name) return;
    } else if (currentView.type === 'dm') {
        if (username !== currentView.name) return;
    } else {
        return;
    }

    typingIndicator.textContent = `${username} is typing...`;
    typingIndicator.classList.remove('hidden');

    clearTimeout(typingTimers[username]);
    typingTimers[username] = setTimeout(() => {
        typingIndicator.classList.add('hidden');
    }, 2000);
}

// ── Image Upload ──
async function uploadAndSendImage() {
    if (!pendingImage) return;

    const formData = new FormData();
    formData.append('image', pendingImage.file);

    imageUploadBtn.disabled = true;
    imageUploadBtn.textContent = '...';

    try {
        const res = await fetch('/api/upload', { method: 'POST', body: formData });
        if (!res.ok) {
            const text = await res.text();
            throw new Error(text);
        }
        const data = await res.json();
        const caption = messageInput.value.trim();

        if (currentView.type === 'room') {
            send({ type: 'image', room: currentView.name, image_url: data.url, content: caption, message_id: generateMsgId() });
        } else {
            const msgId = generateMsgId();
            send({ type: 'image', to: currentView.name, image_url: data.url, content: caption, message_id: msgId });
            // Pre-store image DM locally (server echo is skipped for own DMs)
            const dmKey = `dm:${currentView.name}`;
            storeAndRender(dmKey, {
                type: 'dm', from: myUsername, to: currentView.name,
                content: caption, image_url: data.url,
                ts: Date.now(), message_id: msgId, status: 'sent',
            });
        }

        messageInput.value = '';
        clearImagePreview();
    } catch (err) {
        addSystemMessage('Failed to upload image: ' + err.message);
    } finally {
        imageUploadBtn.disabled = false;
        imageUploadBtn.textContent = '📷';
    }
}

function showImagePreview(file) {
    const url = URL.createObjectURL(file);
    pendingImage = { file, previewURL: url };
    imagePreviewThumb.src = url;
    imagePreviewName.textContent = file.name;
    imagePreviewBar.classList.remove('hidden');
    messageInput.placeholder = 'Add a caption (optional)...';
}

function clearImagePreview() {
    if (pendingImage) {
        URL.revokeObjectURL(pendingImage.previewURL);
        pendingImage = null;
    }
    imagePreviewBar.classList.add('hidden');
    imageFileInput.value = '';
    messageInput.placeholder = 'Type a message...';
}

// ── Image Lightbox ──
function openLightbox(url) {
    lightboxImg.src = url;
    imageLightbox.classList.remove('hidden');
}

function closeLightbox() {
    imageLightbox.classList.add('hidden');
    lightboxImg.src = '';
}

// ── Emoji Picker ──
function buildEmojiPicker() {
    emojiPicker.innerHTML = '';
    const tabs = document.createElement('div');
    tabs.className = 'emoji-tabs';
    const grid = document.createElement('div');
    grid.className = 'emoji-grid';

    const categories = Object.keys(EMOJI_CATEGORIES);
    categories.forEach((cat, i) => {
        const tab = document.createElement('button');
        tab.className = 'emoji-tab' + (i === 0 ? ' active' : '');
        tab.textContent = EMOJI_CATEGORIES[cat][0];
        tab.title = cat;
        tab.addEventListener('click', () => {
            tabs.querySelectorAll('.emoji-tab').forEach(t => t.classList.remove('active'));
            tab.classList.add('active');
            renderEmojiGrid(grid, cat);
        });
        tabs.appendChild(tab);
    });

    emojiPicker.appendChild(tabs);
    emojiPicker.appendChild(grid);
    renderEmojiGrid(grid, categories[0]);
}

function renderEmojiGrid(container, category) {
    container.innerHTML = '';
    EMOJI_CATEGORIES[category].forEach(emoji => {
        const btn = document.createElement('button');
        btn.className = 'emoji-item';
        btn.textContent = emoji;
        btn.addEventListener('click', () => {
            messageInput.value += emoji;
            messageInput.focus();
        });
        container.appendChild(btn);
    });
}

function toggleEmojiPicker() {
    if (emojiPicker.classList.contains('hidden')) {
        if (!emojiPicker.hasChildNodes()) buildEmojiPicker();
        emojiPicker.classList.remove('hidden');
    } else {
        emojiPicker.classList.add('hidden');
    }
}

function closeEmojiPicker() {
    emojiPicker.classList.add('hidden');
}

// ── Notifications ──
function requestNotificationPermission() {
    if ('Notification' in window) {
        if (Notification.permission === 'granted') {
            notificationsEnabled = true;
        } else if (Notification.permission !== 'denied') {
            Notification.requestPermission().then(perm => {
                notificationsEnabled = (perm === 'granted');
            });
        }
    }
}

function showBrowserNotification(title, body, tag, onClick) {
    if (!notificationsEnabled || tabFocused) return;
    try {
        const notif = new Notification(title, {
            body: body,
            icon: '/icons/icon-192.png',
            tag: tag || 'wifi-chat',
            renotify: true,
            silent: false,
        });
        notif.onclick = () => {
            window.focus();
            notif.close();
            if (onClick) onClick();
        };
        // Auto-close after 5 seconds
        setTimeout(() => notif.close(), 5000);
    } catch(e) {
        // Notification constructor may fail in some contexts
    }
}

function notifyMessage(from, content, isDM, roomOrUser) {
    if (from === myUsername) return;
    const title = isDM ? `DM from ${from}` : `#${roomOrUser}`;
    const body = isDM ? content : `${from}: ${content}`;
    const tag = isDM ? `dm-${from}` : `room-${roomOrUser}`;
    showBrowserNotification(title, body, tag, () => {
        if (isDM) {
            switchToDM(from);
        } else {
            switchToRoom(roomOrUser);
        }
    });
}

function notifyIncomingCall(from, callType) {
    const typeLabel = callType === 'video' ? 'Video' : 'Voice';
    showBrowserNotification(
        `Incoming ${typeLabel} Call`,
        `${from} is calling you`,
        'incoming-call',
        () => { window.focus(); }
    );
}

function playNotificationSound() {
    try {
        const ctx = new (window.AudioContext || window.webkitAudioContext)();
        const osc = ctx.createOscillator();
        const gain = ctx.createGain();
        osc.connect(gain);
        gain.connect(ctx.destination);
        osc.frequency.value = 800;
        osc.type = 'sine';
        gain.gain.setValueAtTime(0.1, ctx.currentTime);
        gain.gain.exponentialRampToValueAtTime(0.001, ctx.currentTime + 0.3);
        osc.start(ctx.currentTime);
        osc.stop(ctx.currentTime + 0.3);
    } catch(e) {}
}

function playRingtone() {
    try {
        const ctx = new (window.AudioContext || window.webkitAudioContext)();
        const playTone = (freq, start, dur) => {
            const osc = ctx.createOscillator();
            const gain = ctx.createGain();
            osc.connect(gain);
            gain.connect(ctx.destination);
            osc.frequency.value = freq;
            osc.type = 'sine';
            gain.gain.setValueAtTime(0.15, ctx.currentTime + start);
            gain.gain.exponentialRampToValueAtTime(0.001, ctx.currentTime + start + dur);
            osc.start(ctx.currentTime + start);
            osc.stop(ctx.currentTime + start + dur);
        };
        // Two-tone ring pattern
        playTone(880, 0, 0.2);
        playTone(660, 0.25, 0.2);
        playTone(880, 0.5, 0.2);
        playTone(660, 0.75, 0.2);
    } catch(e) {}
}

function updateTitleBadge() {
    if (!tabFocused) {
        totalUnread++;
    }
    if (totalUnread > 0) {
        document.title = `(${totalUnread}) ${BASE_TITLE}`;
    } else {
        document.title = BASE_TITLE;
    }
}

// ── BitChat-inspired IRC-style slash commands ──
function handleSlashCommand(input) {
    const parts = input.split(/\s+/);
    const cmd = parts[0].toLowerCase();
    switch (cmd) {
        case '/who':
        case '/w':
            addSystemMessage(`Online users: ${users.join(', ') || 'none'}`);
            return true;
        case '/clear':
            if (currentView.type === 'room') {
                delete messageStore[currentView.name];
            } else {
                delete messageStore[`dm:${currentView.name}`];
            }
            saveMessageStore();
            renderMessages();
            addSystemMessage('Chat cleared locally.');
            return true;
        case '/msg':
        case '/m': {
            const target = (parts[1] || '').replace(/^@/, '');
            if (!target) { addSystemMessage('Usage: /msg @username [message]'); return true; }
            switchToDM(target);
            if (parts.length > 2) {
                const msgContent = parts.slice(2).join(' ');
                messageInput.value = msgContent;
            }
            return true;
        }
        case '/slap': {
            const target = (parts[1] || '').replace(/^@/, '');
            if (!target) { addSystemMessage('Usage: /slap @username'); return true; }
            const slapMsg = `slaps ${target} around a bit with a large trout 🐟`;
            const slapId = generateMsgId();
            if (currentView.type === 'room') {
                send({ type: 'message', room: currentView.name, content: slapMsg, message_id: slapId });
            } else {
                send({ type: 'dm', to: currentView.name, content: slapMsg, message_id: slapId });
                storeAndRender(`dm:${currentView.name}`, {
                    type: 'dm', from: myUsername, to: currentView.name,
                    content: slapMsg, ts: Date.now(), message_id: slapId, status: 'sent',
                });
            }
            return true;
        }
        case '/hug': {
            const target = (parts[1] || '').replace(/^@/, '');
            if (!target) { addSystemMessage('Usage: /hug @username'); return true; }
            const hugMsg = `gives ${target} a warm hug 🫂`;
            const hugId = generateMsgId();
            if (currentView.type === 'room') {
                send({ type: 'message', room: currentView.name, content: hugMsg, message_id: hugId });
            } else {
                send({ type: 'dm', to: currentView.name, content: hugMsg, message_id: hugId });
                storeAndRender(`dm:${currentView.name}`, {
                    type: 'dm', from: myUsername, to: currentView.name,
                    content: hugMsg, ts: Date.now(), message_id: hugId, status: 'sent',
                });
            }
            return true;
        }
        case '/wipe':
            emergencyWipe();
            return true;
        case '/fingerprint':
        case '/fp': {
            const target = (parts[1] || '').replace(/^@/, '') || myUsername;
            if (typeof cryptoManager !== 'undefined') {
                cryptoManager.getFingerprint(target).then(fp => {
                    addSystemMessage(`🔐 Key fingerprint for ${target}:\n${fp}\n\nCompare with your contact in person to verify identity.`);
                });
            } else {
                addSystemMessage('E2EE not available');
            }
            return true;
        }
        case '/disappear': {
            const minutes = parseInt(parts[1]) || 0;
            if (minutes <= 0) {
                disappearingTimer = 0;
                addSystemMessage('Disappearing messages disabled.');
            } else {
                disappearingTimer = minutes * 60 * 1000;
                addSystemMessage(`Disappearing messages enabled: messages auto-delete after ${minutes} minute(s).`);
                scheduleDisappearingMessages();
            }
            return true;
        }
        case '/help':
            addSystemMessage(
                'Available commands:\n' +
                '/who — List online users\n' +
                '/msg @user [message] — Open DM with user\n' +
                '/clear — Clear current chat locally\n' +
                '/slap @user — Slap someone with a trout\n' +
                '/hug @user — Give someone a hug\n' +
                '/fingerprint [@user] — Show key fingerprint for verification\n' +
                '/disappear [minutes] — Auto-delete messages (0 = off)\n' +
                '/wipe — Emergency wipe all local data\n' +
                '/help — Show this help'
            );
            return true;
        default:
            return false; // not a recognized command, send as message
    }
}

// ── Emergency Wipe (BitChat-inspired) ──
function emergencyWipe() {
    if (!confirm('⚠️ EMERGENCY WIPE\n\nThis will permanently delete ALL local data including messages, encryption keys, and session data.\n\nThis action cannot be undone. Continue?')) {
        return;
    }
    // Clear all storage
    sessionStorage.clear();
    localStorage.clear();
    // Close WebSocket
    if (ws) { ws.close(); ws = null; }
    // Clear in-memory state
    messageStore = {};
    rooms = [];
    users = [];
    roomUsers = {};
    unreadCounts = {};
    // Notify
    document.body.innerHTML = '<div style="display:flex;align-items:center;justify-content:center;height:100vh;font-family:sans-serif;color:#888;"><div style="text-align:center"><h1>🗑️ Data Wiped</h1><p>All local data has been cleared.</p><button onclick="location.reload()" style="padding:10px 24px;border-radius:8px;border:none;background:#5b5ea6;color:white;cursor:pointer;font-size:16px;">Restart</button></div></div>';
}

// ── Briar-inspired: Disappearing Messages ──
let disappearingIntervalId = null;
function scheduleDisappearingMessages() {
    if (disappearingIntervalId) {
        clearInterval(disappearingIntervalId);
        disappearingIntervalId = null;
    }
    if (!disappearingTimer || disappearingTimer <= 0) return;
    disappearingIntervalId = setInterval(() => {
        if (!disappearingTimer || disappearingTimer <= 0) {
            clearInterval(disappearingIntervalId);
            disappearingIntervalId = null;
            return;
        }
        const now = Date.now();
        let changed = false;
        for (const key in messageStore) {
            const msgs = messageStore[key];
            const filtered = msgs.filter(m => {
                if (m.type === 'system') return true;
                return (now - (m.ts || 0)) < disappearingTimer;
            });
            if (filtered.length < msgs.length) {
                messageStore[key] = filtered;
                changed = true;
            }
        }
        if (changed) {
            saveMessageStore();
            renderMessages();
        }
    }, 30000); // check every 30s
}

// ── Sidebar ──
function closeSidebar() { sidebar.classList.remove('open'); }

function showLoginError(msg) {
    loginError.textContent = msg;
    loginError.classList.remove('hidden');
}

function logout() {
    console.log('[DEBUG] Logout clicked');
    // Clear all session data
    sessionStorage.removeItem('wifi-chat-username');
    sessionStorage.removeItem('wifi-chat-view');
    sessionStorage.removeItem('wifi-chat-messages');
    sessionStorage.removeItem('e2ee-privkey');
    sessionStorage.removeItem('e2ee-pubkey');
    sessionStorage.removeItem('e2ee-pubkey-b64');
    sessionStorage.removeItem('e2ee-peers');
    // Close WebSocket
    if (ws) {
        ws.close();
        ws = null;
    }
    // Reload page to reset state
    location.reload();
}

// ── DOM Init & Event Listeners ──
function initDOM() {
    loginScreen       = document.getElementById('login-screen');
    chatScreen        = document.getElementById('chat-screen');
    loginForm         = document.getElementById('login-form');
    usernameInput     = document.getElementById('username-input');
    loginError        = document.getElementById('login-error');
    messagesDiv       = document.getElementById('messages');
    messageForm       = document.getElementById('message-form');
    messageInput      = document.getElementById('message-input');
    chatTitle         = document.getElementById('chat-title');
    chatSubtitle      = document.getElementById('chat-subtitle');
    roomListEl        = document.getElementById('room-list');
    dmListEl          = document.getElementById('dm-list');
    userListEl        = document.getElementById('user-list');
    onlineCount       = document.getElementById('online-count');
    myUsernameEl      = document.getElementById('my-username');
    typingIndicator   = document.getElementById('typing-indicator');
    createRoomBtn     = document.getElementById('create-room-btn');
    createRoomModal   = document.getElementById('create-room-modal');
    createRoomForm    = document.getElementById('create-room-form');
    roomNameInput     = document.getElementById('room-name-input');
    cancelRoomBtn     = document.getElementById('cancel-room-btn');
    leaveRoomBtn      = document.getElementById('leave-room-btn');
    sidebar           = document.getElementById('sidebar');
    sidebarOpen       = document.getElementById('sidebar-open');
    sidebarClose      = document.getElementById('sidebar-close');
    messagesContainer = document.getElementById('messages-container');
    logoutBtn         = document.getElementById('logout-btn');
    imageUploadBtn    = document.getElementById('image-upload-btn');
    imageFileInput    = document.getElementById('image-file-input');
    imagePreviewBar   = document.getElementById('image-preview-bar');
    imagePreviewThumb = document.getElementById('image-preview-thumb');
    imagePreviewName  = document.getElementById('image-preview-name');
    imagePreviewCancel= document.getElementById('image-preview-cancel');
    emojiBtn          = document.getElementById('emoji-btn');
    emojiPicker       = document.getElementById('emoji-picker');
    imageLightbox     = document.getElementById('image-lightbox');
    lightboxImg       = document.getElementById('lightbox-img');
    lightboxClose     = document.getElementById('lightbox-close');

    // ── Event Listeners ──
    console.log('[DEBUG] initDOM() called, loginForm:', !!loginForm);
    loginForm.addEventListener('submit', (e) => {
        e.preventDefault();
        console.log('[DEBUG] Login form submitted');
        const username = usernameInput.value.trim();
        if (!username) { console.log('[DEBUG] Empty username, returning'); return; }
        console.log('[DEBUG] Username:', username);
        myUsername = username;
        window.myUsername = username;
        myUsernameEl.textContent = username;
        // Save to sessionStorage (tab-specific, not shared across tabs)
        sessionStorage.setItem('wifi-chat-username', username);
        requestNotificationPermission();
        connect(username);
    });

    messageForm.addEventListener('submit', (e) => {
        e.preventDefault();
        if (pendingImage) {
            uploadAndSendImage();
            return;
        }
        const content = messageInput.value.trim();
        if (!content) return;

        // BitChat-inspired IRC-style slash commands
        if (content.startsWith('/')) {
            if (handleSlashCommand(content)) {
                messageInput.value = '';
                closeEmojiPicker();
                return;
            }
        }

        if (currentView.type === 'room') {
            const msgId = generateMsgId();
            send({ type: 'message', room: currentView.name, content, message_id: msgId });
        } else {
            // DMs: encrypt if possible, store plaintext locally
            const target = currentView.name;
            (async () => {
                const msgId = generateMsgId();
                const encrypted = await cryptoManager.encrypt(target, content);
                if (encrypted) {
                    send({ type: 'dm', to: target, content: encrypted, encrypted: true, message_id: msgId });
                } else {
                    send({ type: 'dm', to: target, content, message_id: msgId });
                }
                // Store plaintext locally (don't wait for server echo)
                const dmKey = `dm:${target}`;
                storeAndRender(dmKey, {
                    type: 'dm', from: myUsername, to: target,
                    content: content, ts: Date.now(), encrypted: !!encrypted,
                    message_id: msgId, status: 'sent',
                });
            })();
        }

        messageInput.value = '';
        closeEmojiPicker();
    });

    messageInput.addEventListener('input', () => {
        clearTimeout(typingTimeout);
        typingTimeout = setTimeout(sendTyping, 300);
    });

    // Image upload
    imageUploadBtn.addEventListener('click', () => imageFileInput.click());
    imageFileInput.addEventListener('change', (e) => {
        const file = e.target.files[0];
        if (file) showImagePreview(file);
    });
    imagePreviewCancel.addEventListener('click', clearImagePreview);

    // Emoji
    emojiBtn.addEventListener('click', toggleEmojiPicker);

    // Lightbox
    lightboxClose.addEventListener('click', closeLightbox);
    imageLightbox.addEventListener('click', (e) => {
        if (e.target === imageLightbox) closeLightbox();
    });

    // Room management
    createRoomBtn.addEventListener('click', () => {
        createRoomModal.classList.remove('hidden');
        roomNameInput.value = '';
        roomNameInput.focus();
    });

    cancelRoomBtn.addEventListener('click', () => {
        createRoomModal.classList.add('hidden');
    });

    createRoomForm.addEventListener('submit', (e) => {
        e.preventDefault();
        const name = roomNameInput.value.trim().toLowerCase().replace(/\s+/g, '-');
        if (!name) return;
        send({ type: 'create_room', room: name });
        createRoomModal.classList.add('hidden');
    });

    leaveRoomBtn.addEventListener('click', () => {
        if (currentView.type === 'room' && currentView.name !== 'general') {
            send({ type: 'leave_room', room: currentView.name });
            delete messageStore[currentView.name];
            switchToRoom('general');
        }
    });

    // Sidebar
    sidebarOpen.addEventListener('click', () => sidebar.classList.add('open'));
    sidebarClose.addEventListener('click', closeSidebar);
    
    // Logout
    logoutBtn.addEventListener('click', logout);

    // Call buttons
    document.getElementById('voice-call-btn').addEventListener('click', () => {
        if (currentView.type === 'dm') callManager.startCall(currentView.name, 'voice');
    });
    document.getElementById('video-call-btn').addEventListener('click', () => {
        if (currentView.type === 'dm') callManager.startCall(currentView.name, 'video');
    });

    createRoomModal.addEventListener('click', (e) => {
        if (e.target === createRoomModal) createRoomModal.classList.add('hidden');
    });

    // Close emoji picker when clicking outside
    document.addEventListener('click', (e) => {
        if (!emojiPicker.contains(e.target) && e.target !== emojiBtn) {
            closeEmojiPicker();
        }
    });

    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') {
            createRoomModal.classList.add('hidden');
            closeLightbox();
            closeEmojiPicker();
        }
    });

    messagesContainer.addEventListener('scroll', () => {
        const scrollBtn = document.getElementById('scroll-bottom-btn');
        if (scrollBtn) {
            const distFromBottom = messagesContainer.scrollHeight - messagesContainer.scrollTop - messagesContainer.clientHeight;
            scrollBtn.classList.toggle('hidden', distFromBottom < 100);
        }
    });

    // Tab focus tracking for title badge
    window.addEventListener('focus', () => {
        tabFocused = true;
        totalUnread = 0;
        updateTitleBadge();
    });
    window.addEventListener('blur', () => {
        tabFocused = false;
    });
}

// ── Initialize when DOM is ready ──
if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
} else {
    init();
}

function init() {
    initDOM();
    // Restore message history
    loadMessageStore();
    // Restore current view from sessionStorage
    const savedView = sessionStorage.getItem('wifi-chat-view');
    if (savedView) {
        try { currentView = JSON.parse(savedView); } catch(e) {}
    }
    // Auto-login if username exists in sessionStorage (tab-specific)
    const savedUsername = sessionStorage.getItem('wifi-chat-username');
    if (savedUsername) {
        console.log('[DEBUG] Auto-login with saved username:', savedUsername);
        myUsername = savedUsername;
        window.myUsername = savedUsername;
        myUsernameEl.textContent = savedUsername;
        requestNotificationPermission();
        connect(savedUsername);
    }
}

// ── PWA Service Worker ──
if ('serviceWorker' in navigator) {
    navigator.serviceWorker.register('/sw.js').catch(() => {});
}

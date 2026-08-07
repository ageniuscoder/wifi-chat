// ── WebRTC Call Manager ──
// Handles voice and video calls over LAN using WebRTC
// No STUN/TURN servers needed — direct peer connection over WiFi

const CallState = { IDLE: 'idle', CALLING: 'calling', RINGING: 'ringing', IN_CALL: 'in_call' };

const callManager = {
    state: CallState.IDLE,
    pc: null,               // RTCPeerConnection
    localStream: null,
    remoteStream: null,
    peer: '',               // username of the other party
    callType: 'voice',      // 'voice' or 'video'
    isMuted: false,
    isCamOff: false,

    // DOM elements (set in init)
    callOverlay: null,
    incomingModal: null,
    localVideo: null,
    remoteVideo: null,
    callPeerName: null,
    callTimer: null,
    callStatus: null,
    incomingFrom: null,
    incomingType: null,
    timerInterval: null,
    callStart: 0,

    _initialized: false,

    init() {
        if (this._initialized) return;
        this._initialized = true;

        this.callOverlay   = document.getElementById('call-overlay');
        this.incomingModal = document.getElementById('incoming-call-modal');
        this.localVideo    = document.getElementById('local-video');
        this.remoteVideo   = document.getElementById('remote-video');
        this.callPeerName  = document.getElementById('call-peer-name');
        this.callTimer     = document.getElementById('call-timer');
        this.callStatus    = document.getElementById('call-status');
        this.incomingFrom  = document.getElementById('incoming-from');
        this.incomingType  = document.getElementById('incoming-type');

        // Button handlers
        document.getElementById('call-mute-btn').addEventListener('click', () => this.toggleMute());
        document.getElementById('call-cam-btn').addEventListener('click', () => this.toggleCamera());
        document.getElementById('call-end-btn').addEventListener('click', () => this.endCall());
        document.getElementById('accept-call-btn').addEventListener('click', () => this.acceptIncoming());
        document.getElementById('reject-call-btn').addEventListener('click', () => this.rejectIncoming());
    },

    // Start an outgoing call
    async startCall(targetUser, type) {
        if (this.state !== CallState.IDLE) {
            addSystemMessage('Already in a call');
            return;
        }

        this.peer = targetUser;
        this.callType = type;
        this.state = CallState.CALLING;

        // Request call
        send({ type: 'call_request', to: targetUser, call_type: type });

        // Show UI
        this.callPeerName.textContent = targetUser;
        this.callStatus.textContent = 'Calling...';
        this.callTimer.textContent = '';
        this.callOverlay.classList.remove('hidden');
        this.callOverlay.dataset.callType = type;

        // Hide camera button for voice calls
        document.getElementById('call-cam-btn').style.display = type === 'video' ? '' : 'none';
    },

    // Handle incoming call request
    onCallRequest(msg) {
        if (this.state !== CallState.IDLE) {
            // Already in a call, auto-reject
            send({ type: 'call_reject', to: msg.from });
            return;
        }

        this.state = CallState.RINGING;
        this.peer = msg.from;
        this.callType = msg.call_type || 'voice';

        this.incomingFrom.textContent = msg.from;
        this.incomingType.textContent = this.callType === 'video' ? '📹 Video Call' : '📞 Voice Call';
        this.incomingModal.classList.remove('hidden');
    },

    // Accept incoming call
    async acceptIncoming() {
        this.incomingModal.classList.add('hidden');

        send({ type: 'call_accept', to: this.peer, call_type: this.callType });

        this.callPeerName.textContent = this.peer;
        this.callStatus.textContent = 'Connecting...';
        this.callTimer.textContent = '';
        this.callOverlay.classList.remove('hidden');
        this.callOverlay.dataset.callType = this.callType;
        document.getElementById('call-cam-btn').style.display = this.callType === 'video' ? '' : 'none';

        this.state = CallState.IN_CALL;
        // Callee waits for offer from caller
    },

    // Reject incoming call
    rejectIncoming() {
        this.incomingModal.classList.add('hidden');
        send({ type: 'call_reject', to: this.peer });
        this.reset();
    },

    // Caller: other party accepted, create offer
    async onCallAccept(msg) {
        if (this.state !== CallState.CALLING) return;
        this.state = CallState.IN_CALL;
        this.callStatus.textContent = 'Connecting...';

        try {
            await this.setupMedia();
            this.createPeerConnection();

            this.localStream.getTracks().forEach(track => {
                this.pc.addTrack(track, this.localStream);
            });

            const offer = await this.pc.createOffer();
            await this.pc.setLocalDescription(offer);

            await this.sendEncrypted({ type: 'call_offer', to: this.peer, sdp: offer.sdp, call_type: this.callType });
        } catch (err) {
            addSystemMessage('Call failed: ' + err.message);
            this.endCall();
        }
    },

    // Callee: received offer, create answer
    async onCallOffer(msg) {
        try {
            msg = await this.decryptSignal(msg);
            await this.setupMedia();
            this.createPeerConnection();

            this.localStream.getTracks().forEach(track => {
                this.pc.addTrack(track, this.localStream);
            });

            await this.pc.setRemoteDescription(new RTCSessionDescription({ type: 'offer', sdp: msg.sdp }));

            const answer = await this.pc.createAnswer();
            await this.pc.setLocalDescription(answer);

            await this.sendEncrypted({ type: 'call_answer', to: this.peer, sdp: answer.sdp });
        } catch (err) {
            addSystemMessage('Call failed: ' + err.message);
            this.endCall();
        }
    },

    // Caller: received answer
    async onCallAnswer(msg) {
        if (!this.pc) return;
        try {
            msg = await this.decryptSignal(msg);
            await this.pc.setRemoteDescription(new RTCSessionDescription({ type: 'answer', sdp: msg.sdp }));
        } catch (err) {
            console.error('Error setting remote description:', err);
        }
    },

    // ICE candidate received
    async onCallICE(msg) {
        if (!this.pc) return;
        try {
            msg = await this.decryptSignal(msg);
            if (!msg.candidate) return;
            await this.pc.addIceCandidate(new RTCIceCandidate(msg.candidate));
        } catch (err) {
            console.error('Error adding ICE candidate:', err);
        }
    },

    // Remote party ended or rejected
    onCallEnd() {
        if (this.state === CallState.RINGING) {
            this.incomingModal.classList.add('hidden');
        }
        addSystemMessage('Call ended');
        this.cleanup();
    },

    onCallReject() {
        addSystemMessage(this.peer + ' declined the call');
        this.cleanup();
    },

    // End call (local action)
    endCall() {
        send({ type: 'call_end', to: this.peer });
        this.cleanup();
    },

    // ── Media & Connection ──

    async setupMedia() {
        const constraints = {
            audio: true,
            video: this.callType === 'video' ? { width: { ideal: 640 }, height: { ideal: 480 } } : false,
        };

        this.localStream = await navigator.mediaDevices.getUserMedia(constraints);
        this.localVideo.srcObject = this.localStream;
        this.localVideo.muted = true; // Don't play own audio
    },

    createPeerConnection() {
        // No ICE servers needed for LAN — host candidates are sufficient
        this.pc = new RTCPeerConnection({ iceServers: [] });

        this.pc.onicecandidate = (event) => {
            if (event.candidate) {
                this.sendEncrypted({
                    type: 'call_ice',
                    to: this.peer,
                    candidate: event.candidate.toJSON(),
                });
            }
        };

        this.pc.ontrack = (event) => {
            this.remoteStream = event.streams[0];
            this.remoteVideo.srcObject = this.remoteStream;
            this.callStatus.textContent = '';
            this.startTimer();
        };

        this.pc.oniceconnectionstatechange = () => {
            if (this.pc.iceConnectionState === 'disconnected' || this.pc.iceConnectionState === 'failed') {
                addSystemMessage('Call connection lost');
                this.cleanup();
            }
        };
    },

    // ── Controls ──

    toggleMute() {
        if (!this.localStream) return;
        this.isMuted = !this.isMuted;
        this.localStream.getAudioTracks().forEach(t => t.enabled = !this.isMuted);
        const btn = document.getElementById('call-mute-btn');
        btn.textContent = this.isMuted ? '🔇' : '🎤';
        btn.classList.toggle('active', this.isMuted);
    },

    toggleCamera() {
        if (!this.localStream) return;
        this.isCamOff = !this.isCamOff;
        this.localStream.getVideoTracks().forEach(t => t.enabled = !this.isCamOff);
        const btn = document.getElementById('call-cam-btn');
        btn.textContent = this.isCamOff ? '🚫' : '📷';
        btn.classList.toggle('active', this.isCamOff);
    },

    // ── Timer ──

    startTimer() {
        this.callStart = Date.now();
        this.timerInterval = setInterval(() => {
            const secs = Math.floor((Date.now() - this.callStart) / 1000);
            const m = String(Math.floor(secs / 60)).padStart(2, '0');
            const s = String(secs % 60).padStart(2, '0');
            this.callTimer.textContent = `${m}:${s}`;
        }, 1000);
    },

    // ── Cleanup ──

    cleanup() {
        if (this.pc) { this.pc.close(); this.pc = null; }
        if (this.localStream) {
            this.localStream.getTracks().forEach(t => t.stop());
            this.localStream = null;
        }
        this.remoteStream = null;
        if (this.localVideo) this.localVideo.srcObject = null;
        if (this.remoteVideo) this.remoteVideo.srcObject = null;
        if (this.timerInterval) { clearInterval(this.timerInterval); this.timerInterval = null; }
        this.callOverlay.classList.add('hidden');
        this.incomingModal.classList.add('hidden');
        this.isMuted = false;
        this.isCamOff = false;
        this.reset();
    },

    reset() {
        this.state = CallState.IDLE;
        this.peer = '';
    },

    // Encrypt outgoing signaling data
    async sendEncrypted(msg) {
        if (this.peer && typeof cryptoManager !== 'undefined' && cryptoManager.hasKeyFor(this.peer)) {
            try {
                if (msg.sdp) {
                    msg.sdp = await cryptoManager.encrypt(this.peer, msg.sdp);
                    msg.encrypted = true;
                }
                if (msg.candidate) {
                    msg.candidate = await cryptoManager.encrypt(this.peer, JSON.stringify(msg.candidate));
                    msg.encrypted = true;
                }
            } catch (e) { console.warn('[E2EE] Call encrypt failed', e); }
        }
        send(msg);
    },

    // Decrypt incoming signaling data
    async decryptSignal(msg) {
        if (msg.encrypted && msg.from && typeof cryptoManager !== 'undefined' && cryptoManager.hasKeyFor(msg.from)) {
            try {
                if (msg.sdp && typeof msg.sdp === 'string') {
                    const dec = await cryptoManager.decrypt(msg.from, msg.sdp);
                    if (dec) msg.sdp = dec;
                }
                if (msg.candidate && typeof msg.candidate === 'string') {
                    const dec = await cryptoManager.decrypt(msg.from, msg.candidate);
                    if (dec) msg.candidate = JSON.parse(dec);
                }
            } catch (e) { console.warn('[E2EE] Call decrypt failed', e); }
        }
        return msg;
    },

    // Handle incoming signaling message
    handleSignal(msg) {
        switch (msg.type) {
            case 'call_request': this.onCallRequest(msg); break;
            case 'call_accept':  this.onCallAccept(msg);  break;
            case 'call_reject':  this.onCallReject(msg);   break;
            case 'call_offer':   this.onCallOffer(msg);    break;
            case 'call_answer':  this.onCallAnswer(msg);   break;
            case 'call_ice':     this.onCallICE(msg);      break;
            case 'call_end':     this.onCallEnd(msg);      break;
        }
    }
};

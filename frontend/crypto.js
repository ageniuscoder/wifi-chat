// ── End-to-End Encryption using Web Crypto API ──
// ECDH P-256 for key exchange, AES-256-GCM for message encryption
// Keys persist in sessionStorage so they survive page refreshes within a tab.
// Gracefully degrades on non-secure contexts (HTTP with non-localhost)
const cryptoManager = {
    keyPair: null,
    publicKeyB64: '',
    peerKeys: {},       // username -> CryptoKey (imported public)
    sharedKeys: {},     // username -> AES-GCM CryptoKey (derived)
    available: !!(window.crypto && window.crypto.subtle),

    async init() {
        if (!this.available) {
            console.warn('[E2EE] crypto.subtle not available (non-secure context). E2EE disabled.');
            return '';
        }

        // Try to restore key pair from sessionStorage
        const savedPriv = sessionStorage.getItem('e2ee-privkey');
        const savedPub = sessionStorage.getItem('e2ee-pubkey');
        const savedPubB64 = sessionStorage.getItem('e2ee-pubkey-b64');

        if (savedPriv && savedPub && savedPubB64) {
            try {
                const privKey = await crypto.subtle.importKey(
                    'jwk', JSON.parse(savedPriv),
                    { name: 'ECDH', namedCurve: 'P-256' },
                    true, ['deriveKey']
                );
                const pubKey = await crypto.subtle.importKey(
                    'jwk', JSON.parse(savedPub),
                    { name: 'ECDH', namedCurve: 'P-256' },
                    true, []
                );
                this.keyPair = { privateKey: privKey, publicKey: pubKey };
                this.publicKeyB64 = savedPubB64;
                console.log('[E2EE] Restored key pair from session');

                // Restore shared keys
                await this._restoreSharedKeys();
                return this.publicKeyB64;
            } catch (e) {
                console.warn('[E2EE] Failed to restore keys, generating new ones', e);
            }
        }

        // Generate new key pair (extractable so we can save to sessionStorage)
        this.keyPair = await crypto.subtle.generateKey(
            { name: 'ECDH', namedCurve: 'P-256' },
            true,
            ['deriveKey']
        );
        const pubRaw = await crypto.subtle.exportKey('raw', this.keyPair.publicKey);
        this.publicKeyB64 = this._bufToB64(pubRaw);

        // Save to sessionStorage
        const privJwk = await crypto.subtle.exportKey('jwk', this.keyPair.privateKey);
        const pubJwk = await crypto.subtle.exportKey('jwk', this.keyPair.publicKey);
        sessionStorage.setItem('e2ee-privkey', JSON.stringify(privJwk));
        sessionStorage.setItem('e2ee-pubkey', JSON.stringify(pubJwk));
        sessionStorage.setItem('e2ee-pubkey-b64', this.publicKeyB64);
        console.log('[E2EE] Generated and saved new key pair');

        return this.publicKeyB64;
    },

    async importPeerKey(username, pubKeyB64) {
        if (!this.available || !this.keyPair) return;
        if (!pubKeyB64 || username === window.myUsername) return;
        if (this.sharedKeys[username]) return; // already derived
        try {
            const raw = this._b64ToBuf(pubKeyB64);
            const pubKey = await crypto.subtle.importKey(
                'raw', raw,
                { name: 'ECDH', namedCurve: 'P-256' },
                false, []
            );
            this.peerKeys[username] = pubKey;
            // Derive shared secret via ECDH, then SHA-256 for AES key
            // This matches Android's E2EEManager derivation for cross-platform interop
            const derivedBits = await crypto.subtle.deriveBits(
                { name: 'ECDH', public: pubKey },
                this.keyPair.privateKey,
                256
            );
            const sha256 = await crypto.subtle.digest('SHA-256', derivedBits);
            this.sharedKeys[username] = await crypto.subtle.importKey(
                'raw', sha256, { name: 'AES-GCM', length: 256 }, true, ['encrypt', 'decrypt']
            );
            // Save peer public key and derived shared key to sessionStorage
            this._savePeerKey(username, pubKeyB64);
        } catch (e) {
            console.warn('[E2EE] Failed to import key for', username, e);
        }
    },

    async encrypt(username, plaintext) {
        if (!this.available) return null;
        const key = this.sharedKeys[username];
        if (!key) return null;
        try {
            const iv = crypto.getRandomValues(new Uint8Array(12));
            const encoded = new TextEncoder().encode(plaintext);
            const cipher = await crypto.subtle.encrypt(
                { name: 'AES-GCM', iv }, key, encoded
            );
            const combined = new Uint8Array(iv.length + cipher.byteLength);
            combined.set(iv);
            combined.set(new Uint8Array(cipher), iv.length);
            return this._bufToB64(combined.buffer);
        } catch (e) {
            console.warn('[E2EE] Encrypt failed:', e);
            return null;
        }
    },

    async decrypt(username, ciphertextB64) {
        if (!this.available) return null;
        const key = this.sharedKeys[username];
        if (!key) return null;
        try {
            const combined = this._b64ToBuf(ciphertextB64);
            const iv = new Uint8Array(combined, 0, 12);
            const ciphertext = new Uint8Array(combined, 12);
            const plain = await crypto.subtle.decrypt(
                { name: 'AES-GCM', iv }, key, ciphertext
            );
            return new TextDecoder().decode(plain);
        } catch (e) {
            console.warn('[E2EE] Decrypt failed:', e);
            return null;
        }
    },

    hasKeyFor(username) {
        return !!this.sharedKeys[username];
    },

    // ── Briar-inspired: Key fingerprint for contact verification ──
    // Returns a human-readable fingerprint of a user's public key
    // Users can compare fingerprints in person to verify identity
    async getFingerprint(username) {
        if (!this.available) return '';
        let keyB64;
        if (username === window.myUsername) {
            keyB64 = this.publicKeyB64;
        } else {
            const stored = JSON.parse(sessionStorage.getItem('e2ee-peers') || '{}');
            keyB64 = stored[username];
        }
        if (!keyB64) return 'No key available';
        const raw = this._b64ToBuf(keyB64);
        const hash = await crypto.subtle.digest('SHA-256', raw);
        const arr = new Uint8Array(hash);
        const hex = Array.from(arr).map(b => b.toString(16).padStart(2, '0')).join('');
        // Format as groups of 4 hex chars
        return hex.match(/.{1,4}/g).join(' ').toUpperCase();
    },

    // Save a peer's public key to sessionStorage for re-derivation after refresh
    _savePeerKey(username, pubKeyB64) {
        try {
            const stored = JSON.parse(sessionStorage.getItem('e2ee-peers') || '{}');
            stored[username] = pubKeyB64;
            sessionStorage.setItem('e2ee-peers', JSON.stringify(stored));
        } catch(e) {}
    },

    // Restore shared keys from saved peer public keys after refresh
    async _restoreSharedKeys() {
        try {
            const stored = JSON.parse(sessionStorage.getItem('e2ee-peers') || '{}');
            for (const [username, pubKeyB64] of Object.entries(stored)) {
                if (username === window.myUsername) continue;
                try {
                    const raw = this._b64ToBuf(pubKeyB64);
                    const pubKey = await crypto.subtle.importKey(
                        'raw', raw,
                        { name: 'ECDH', namedCurve: 'P-256' },
                        false, []
                    );
                    this.peerKeys[username] = pubKey;
                    // ECDH + SHA-256 derivation (matching Android E2EEManager)
                    const derivedBits = await crypto.subtle.deriveBits(
                        { name: 'ECDH', public: pubKey },
                        this.keyPair.privateKey,
                        256
                    );
                    const sha256 = await crypto.subtle.digest('SHA-256', derivedBits);
                    this.sharedKeys[username] = await crypto.subtle.importKey(
                        'raw', sha256, { name: 'AES-GCM', length: 256 }, true, ['encrypt', 'decrypt']
                    );
                    console.log('[E2EE] Restored shared key for', username);
                } catch (e) {
                    console.warn('[E2EE] Failed to restore key for', username, e);
                }
            }
        } catch(e) {}
    },

    _bufToB64(buf) {
        return btoa(String.fromCharCode(...new Uint8Array(buf)));
    },

    _b64ToBuf(b64) {
        const bin = atob(b64);
        const buf = new ArrayBuffer(bin.length);
        const arr = new Uint8Array(buf);
        for (let i = 0; i < bin.length; i++) arr[i] = bin.charCodeAt(i);
        return buf;
    }
};

// ── Code Detection & Syntax Highlighting ──
// Detects code blocks (```lang ... ```) and inline code (`...`) in messages,
// and auto-detects language from content heuristics.
// Renders with syntax highlighting using a lightweight built-in tokenizer.

const codeFormatter = {
    // Language detection patterns
    langPatterns: {
        go:         [/\bfunc\s+\w+\s*\(/, /\bpackage\s+\w+/, /\bfmt\./, /\b:=\b/, /\bgo\s+func\b/, /\bdefer\b/, /\bchan\b/, /\bgoroutine\b/],
        python:     [/\bdef\s+\w+\s*\(/, /\bimport\s+\w+/, /\bfrom\s+\w+\s+import/, /\bprint\s*\(/, /\bclass\s+\w+.*:/, /\bself\.\w+/, /\belif\b/],
        javascript: [/\bconst\s+\w+\s*=/, /\blet\s+\w+\s*=/, /\bfunction\s+\w+\s*\(/, /\b=>\s*[{(]/, /\bconsole\.\w+/, /\bdocument\.\w+/, /\bwindow\.\w+/],
        typescript: [/\binterface\s+\w+/, /\btype\s+\w+\s*=/, /:\s*(string|number|boolean|any)\b/, /\bas\s+\w+/],
        java:       [/\bpublic\s+(class|static|void)/, /\bSystem\.out\.print/, /\bString\[\]\s+args/, /\bprivate\s+(final\s+)?/],
        kotlin:     [/\bfun\s+\w+\s*\(/, /\bval\s+\w+/, /\bvar\s+\w+/, /\bdata\s+class\b/, /\bsuspend\s+fun/, /\bcompanion\s+object/],
        rust:       [/\bfn\s+\w+\s*\(/, /\blet\s+mut\s+/, /\bimpl\s+\w+/, /\bmatch\s+\w+/, /\b->\s+\w+/, /\bpub\s+(fn|struct|enum)/],
        c:          [/\b(int|char|void|float|double)\s+\w+\s*\(/, /\b#include\s*</, /\bprintf\s*\(/, /\bmalloc\s*\(/],
        cpp:        [/\bstd::/, /\bcout\s*<</, /\bvector</, /\btemplate\s*</, /\bclass\s+\w+\s*\{/],
        html:       [/<\/?[a-z]+[^>]*>/i, /<!DOCTYPE/i, /<html/i, /<div\b/i],
        css:        [/\{[^}]*:\s*[^;]+;/, /\.([\w-]+)\s*\{/, /#[\w-]+\s*\{/, /@media\s/],
        sql:        [/\bSELECT\s+/i, /\bFROM\s+/i, /\bWHERE\s+/i, /\bINSERT\s+INTO/i, /\bCREATE\s+TABLE/i],
        bash:       [/^\s*#!\/bin\/(bash|sh)/, /\becho\s+/, /\bsudo\s+/, /\bgrep\s+/, /\bawk\s+/, /\|\s*\w+/],
        json:       [/^\s*\{[\s\S]*"[\w]+":\s*/, /^\s*\[[\s\S]*\{/],
        yaml:       [/^\s*\w+:\s*\n/, /^\s*-\s+\w+:/m],
        swift:      [/\bfunc\s+\w+\s*\(.*\)\s*->/, /\bvar\s+\w+\s*:\s*\w+/, /\bguard\s+let\b/, /\bimport\s+UIKit/],
    },

    // Token patterns for syntax highlighting (language-agnostic, good enough for most)
    tokenRules: [
        // Comments
        { pattern: /(\/\/[^\n]*)/g, className: 'code-comment' },
        { pattern: /(#[^\n]*)/g, className: 'code-comment' },
        { pattern: /(\/\*[\s\S]*?\*\/)/g, className: 'code-comment' },
        // Strings
        { pattern: /("(?:[^"\\]|\\.)*")/g, className: 'code-string' },
        { pattern: /('(?:[^'\\]|\\.)*')/g, className: 'code-string' },
        { pattern: /(`(?:[^`\\]|\\.)*`)/g, className: 'code-string' },
        // Numbers
        { pattern: /\b(\d+\.?\d*(?:e[+-]?\d+)?)\b/gi, className: 'code-number' },
        // Keywords (common across languages)
        { pattern: /\b(func|function|def|class|struct|enum|interface|type|const|let|var|val|if|else|elif|for|while|do|switch|case|default|return|break|continue|import|from|package|pub|fn|impl|match|async|await|try|catch|finally|throw|throws|new|delete|this|self|super|true|false|nil|null|None|void|int|float|double|string|bool|boolean|byte|char|long|short|unsigned|signed|static|final|private|public|protected|internal|override|abstract|virtual|extends|implements|defer|go|chan|select|goroutine|suspend|companion|data|sealed|object|when|guard|where|in|out|yield|lambda|raise|except|pass|with|as|is|not|and|or|println|printf|fmt|println|print|log|console)\b/g, className: 'code-keyword' },
        // Types (common)
        { pattern: /\b(String|Int|Float|Double|Boolean|List|Map|Set|Array|HashMap|ArrayList|Vec|Option|Result|Error|Context|Mutex|Channel|Promise|Observable)\b/g, className: 'code-type' },
        // Function calls
        { pattern: /\b([a-zA-Z_]\w*)\s*\(/g, className: 'code-function' },
        // Decorators/annotations
        { pattern: /(@\w+)/g, className: 'code-decorator' },
    ],

    /**
     * Detect language from code content heuristics
     */
    detectLanguage(code) {
        let best = '';
        let bestScore = 0;
        for (const [lang, patterns] of Object.entries(this.langPatterns)) {
            let score = 0;
            for (const p of patterns) {
                if (p.test(code)) score++;
            }
            if (score > bestScore) {
                bestScore = score;
                best = lang;
            }
        }
        return bestScore >= 1 ? best : '';
    },

    /**
     * Apply syntax highlighting to code text (returns HTML)
     */
    highlight(code, lang) {
        // Escape HTML first
        let html = this.escapeHtml(code);

        // Apply token rules in a safe way using placeholder replacement
        const replacements = [];

        // Collect all matches with positions
        for (const rule of this.tokenRules) {
            const regex = new RegExp(rule.pattern.source, rule.pattern.flags);
            let match;
            while ((match = regex.exec(html)) !== null) {
                replacements.push({
                    start: match.index,
                    end: match.index + match[0].length,
                    text: match[1] || match[0],
                    className: rule.className,
                    fullMatch: match[0],
                });
            }
        }

        // Sort by position, longest first to avoid overlaps
        replacements.sort((a, b) => a.start - b.start || b.end - a.end);

        // Remove overlapping replacements (keep first/longest)
        const filtered = [];
        let lastEnd = -1;
        for (const r of replacements) {
            if (r.start >= lastEnd) {
                filtered.push(r);
                lastEnd = r.end;
            }
        }

        // Apply replacements in reverse to preserve positions
        for (let i = filtered.length - 1; i >= 0; i--) {
            const r = filtered[i];
            const before = html.slice(0, r.start);
            const after = html.slice(r.end);
            const highlighted = r.fullMatch.replace(
                r.text,
                `<span class="${r.className}">${r.text}</span>`
            );
            html = before + highlighted + after;
        }

        return html;
    },

    escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    },

    /**
     * Format message content: detect code blocks, inline code, and render
     * Returns { html: string, hasCode: boolean }
     */
    formatMessage(content) {
        if (!content) return { html: '', hasCode: false };

        let hasCode = false;

        // Process fenced code blocks: ```lang\n...\n```
        const fencedRegex = /```(\w*)\n?([\s\S]*?)```/g;
        let result = content;
        const blocks = [];
        let blockIdx = 0;

        result = result.replace(fencedRegex, (match, lang, code) => {
            hasCode = true;
            const detectedLang = lang || this.detectLanguage(code.trim());
            const highlighted = this.highlight(code.trimEnd(), detectedLang);
            const langLabel = detectedLang ? `<span class="code-lang-label">${detectedLang}</span>` : '';
            const placeholder = `\x00CODEBLOCK${blockIdx}\x00`;
            blocks.push(`<div class="code-block">${langLabel}<button class="code-copy-btn" onclick="codeFormatter.copyCode(this)" title="Copy">📋</button><pre><code>${highlighted}</code></pre></div>`);
            blockIdx++;
            return placeholder;
        });

        // Process inline code: `...` (use placeholders like fenced blocks to avoid XSS)
        const inlineBlocks = [];
        let inlineIdx = 0;
        result = result.replace(/`([^`\n]+)`/g, (match, code) => {
            hasCode = true;
            const placeholder = `\x00INLINE${inlineIdx}\x00`;
            inlineBlocks.push(`<code class="inline-code">${this.escapeHtml(code)}</code>`);
            inlineIdx++;
            return placeholder;
        });

        // Auto-detect code if message looks like code (no markdown markers)
        if (!hasCode && this.looksLikeCode(content)) {
            hasCode = true;
            const detectedLang = this.detectLanguage(content);
            const highlighted = this.highlight(content.trim(), detectedLang);
            const langLabel = detectedLang ? `<span class="code-lang-label">${detectedLang}</span>` : '';
            return {
                html: `<div class="code-block">${langLabel}<button class="code-copy-btn" onclick="codeFormatter.copyCode(this)" title="Copy">📋</button><pre><code>${highlighted}</code></pre></div>`,
                hasCode: true,
            };
        }

        // Escape remaining non-code text (but not placeholders)
        if (!hasCode) {
            result = this.escapeHtml(result);
        } else {
            // Escape text parts but preserve code block and inline placeholders
            const parts = result.split(/(\x00CODEBLOCK\d+\x00|\x00INLINE\d+\x00)/);
            result = parts.map(part => {
                if (part.startsWith('\x00CODEBLOCK') || part.startsWith('\x00INLINE')) return part;
                return this.escapeHtml(part);
            }).join('');
        }

        // Restore code blocks and inline code
        for (let i = 0; i < blocks.length; i++) {
            result = result.replace(`\x00CODEBLOCK${i}\x00`, blocks[i]);
        }
        for (let i = 0; i < inlineBlocks.length; i++) {
            result = result.replace(`\x00INLINE${i}\x00`, inlineBlocks[i]);
        }

        // Convert newlines to <br> in non-code parts
        result = result.replace(/\n/g, '<br>');

        return { html: result, hasCode };
    },

    /**
     * Heuristic: does this look like pasted code?
     */
    looksLikeCode(text) {
        if (text.length < 20) return false;
        const lines = text.split('\n');
        if (lines.length < 2) return false;

        let codeSignals = 0;
        // Indentation pattern
        if (lines.filter(l => /^\s{2,}/.test(l)).length > lines.length * 0.3) codeSignals++;
        // Semicolons or braces
        if (/[{};]/.test(text)) codeSignals++;
        // Function/variable declarations
        if (/\b(func|function|def|class|const|let|var|val|fn|pub|import|package|#include)\b/.test(text)) codeSignals++;
        // Operators
        if (/(:=|=>|->|!=|==|&&|\|\|)/.test(text)) codeSignals++;
        // Parentheses with identifiers
        if (/\w+\([^)]*\)/.test(text)) codeSignals++;

        return codeSignals >= 2;
    },

    /**
     * Copy code block content to clipboard
     */
    copyCode(btn) {
        const pre = btn.parentElement.querySelector('pre code');
        if (!pre) return;
        const text = pre.textContent;
        navigator.clipboard.writeText(text).then(() => {
            btn.textContent = '✓';
            btn.classList.add('copied');
            setTimeout(() => { btn.textContent = '📋'; btn.classList.remove('copied'); }, 2000);
        }).catch(() => {
            btn.textContent = '✗';
            setTimeout(() => { btn.textContent = '📋'; }, 2000);
        });
    },
};

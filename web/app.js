(() => {
    let problems = [];
    let currentProblem = null;
    let currentLang = localStorage.getItem('sj_lang') || 'cpp';
    let editor = null;
    let activeEventSource = null;
    let catalogSearchQuery = '';
    let catalogStatusFilter = 'all';

    const sessionUserId = (() => {
        let uid = localStorage.getItem('sj_user_id');
        if (!uid) {
            uid = 'user_' + Math.random().toString(36).substring(2, 10);
            localStorage.setItem('sj_user_id', uid);
        }
        return uid;
    })();

    const sessionHistory = JSON.parse(localStorage.getItem('sj_history') || '[]');

    const catalogView = document.getElementById('catalogView');
    const workspaceView = document.getElementById('workspaceView');
    const navCatalogBtn = document.getElementById('navCatalogBtn');
    const navWorkspaceBtn = document.getElementById('navWorkspaceBtn');
    const workspaceSubmitWrap = document.getElementById('workspaceSubmitWrap');
    const submitBtn = document.getElementById('submitBtn');
    const resetCodeBtn = document.getElementById('resetCodeBtn');
    const catalogSearchInput = document.getElementById('catalogSearchInput');

    const monacoLangMap = {
        cpp: 'cpp',
        python: 'python',
        go: 'go'
    };

    function getProblemStatusMap() {
        const map = new Map();
        sessionHistory.forEach((item) => {
            const prev = map.get(item.question_id);
            if (item.status === 'ACCEPTED') {
                map.set(item.question_id, 'solved');
            } else if (prev !== 'solved') {
                map.set(item.question_id, 'attempted');
            }
        });
        return map;
    }

    function splitTitle(fullTitle) {
        const parts = (fullTitle || '').split('·');
        if (parts.length >= 2) {
            return {
                code: parts[0].trim(),
                name: parts.slice(1).join('·').trim()
            };
        }
        return { code: '', name: fullTitle };
    }

    function switchView(viewMode) {
        if (viewMode === 'workspace') {
            catalogView.classList.add('hidden');
            workspaceView.classList.remove('hidden');
            workspaceSubmitWrap.classList.remove('hidden');
            navCatalogBtn.classList.remove('active');
            navWorkspaceBtn.classList.add('active');
            if (editor) {
                setTimeout(() => editor.layout(), 20);
            }
        } else {
            workspaceView.classList.add('hidden');
            catalogView.classList.remove('hidden');
            workspaceSubmitWrap.classList.add('hidden');
            navWorkspaceBtn.classList.remove('active');
            navCatalogBtn.classList.add('active');
            renderCatalog();
        }
    }

    function handleRoute() {
        const hash = window.location.hash || '#/';
        if (hash.startsWith('#/problem/')) {
            const probId = decodeURIComponent(hash.replace('#/problem/', '').trim());
            if (probId) {
                switchView('workspace');
                selectProblem(probId);
                return;
            }
        }
        switchView('catalog');
    }

    function renderCatalog() {
        const statusMap = getProblemStatusMap();
        let solvedTotal = 0;
        let attemptedTotal = 0;

        problems.forEach((p) => {
            const st = statusMap.get(p.id);
            if (st === 'solved') solvedTotal++;
            else if (st === 'attempted') attemptedTotal++;
        });

        document.getElementById('statSolvedCount').textContent = solvedTotal;
        document.getElementById('statAttemptedCount').textContent = attemptedTotal;
        document.getElementById('statTotalCount').textContent = problems.length;

        const pct = problems.length > 0 ? Math.round((solvedTotal / problems.length) * 100) : 0;
        document.getElementById('catalogProgressBar').style.width = `${pct}%`;

        const query = catalogSearchQuery.toLowerCase().trim();
        const grouped = new Map();

        problems.forEach((p) => {
            const st = statusMap.get(p.id) || 'unsolved';
            if (catalogStatusFilter === 'solved' && st !== 'solved') return;
            if (catalogStatusFilter === 'unsolved' && st === 'solved') return;

            if (query) {
                const hay = `${p.title} ${p.category} ${p.id}`.toLowerCase();
                if (!hay.includes(query)) return;
            }

            const cat = p.category || 'General';
            if (!grouped.has(cat)) grouped.set(cat, []);
            grouped.get(cat).push(p);
        });

        const navList = document.getElementById('topicNavList');
        const sectionsContainer = document.getElementById('catalogSectionsContainer');
        navList.innerHTML = '';
        sectionsContainer.innerHTML = '';

        if (grouped.size === 0) {
            sectionsContainer.innerHTML = `
                <div class="topic-section-card" style="padding: 36px; text-align: center; color: var(--ink-muted);">
                    No problems match your current filter criteria.
                </div>
            `;
            return;
        }

        let sectionIdx = 1;
        grouped.forEach((items, category) => {
            const secId = `topic-sec-${sectionIdx}`;
            let catSolved = 0;
            items.forEach((item) => {
                if (statusMap.get(item.id) === 'solved') catSolved++;
            });

            const navLink = document.createElement('a');
            navLink.className = 'topic-nav-item';
            navLink.innerHTML = `
                <span>${escapeHtml(category)}</span>
                <span class="topic-nav-count">${catSolved}/${items.length}</span>
            `;
            navLink.addEventListener('click', (e) => {
                e.preventDefault();
                const targetEl = document.getElementById(secId);
                if (targetEl) targetEl.scrollIntoView({ behavior: 'smooth', block: 'start' });
            });
            navList.appendChild(navLink);

            const sectionCard = document.createElement('div');
            sectionCard.className = 'topic-section-card';
            sectionCard.id = secId;

            const idxStr = String(sectionIdx).padStart(2, '0');
            sectionCard.innerHTML = `
                <div class="topic-section-header">
                    <div class="topic-section-title-group">
                        <span class="topic-index-badge">${idxStr}</span>
                        <h2 class="topic-section-title">${escapeHtml(category)}</h2>
                    </div>
                    <span class="topic-section-progress">${catSolved} / ${items.length} Solved</span>
                </div>
                <div class="topic-tasks-grid"></div>
            `;

            const grid = sectionCard.querySelector('.topic-tasks-grid');
            items.forEach((p) => {
                const st = statusMap.get(p.id) || '';
                const parsed = splitTitle(p.title);
                const tlSec = ((p.time_limit_ms || 1000) / 1000).toFixed(1) + 's';
                const mlMB = (p.memory_limit_mb || 512) + 'M';

                const row = document.createElement('a');
                row.className = 'task-row';
                row.href = `#/problem/${encodeURIComponent(p.id)}`;
                row.innerHTML = `
                    <div class="task-row-left">
                        <span class="task-status-indicator ${st}"></span>
                        ${parsed.code ? `<span class="task-code">#${escapeHtml(parsed.code)}</span>` : ''}
                        <span class="task-name">${escapeHtml(parsed.name)}</span>
                    </div>
                    <div class="task-row-right">
                        <span class="task-meta-pill">${tlSec} · ${mlMB}</span>
                    </div>
                `;
                grid.appendChild(row);
            });

            sectionsContainer.appendChild(sectionCard);
            sectionIdx++;
        });
    }

    function renderMathString(rawStr) {
        const escaped = escapeHtml(rawStr || '');
        if (!window.katex) return escaped;

        const withDisplay = escaped.replace(/\$\$([\s\S]+?)\$\$/g, (_, tex) => {
            try {
                return window.katex.renderToString(tex.trim(), { displayMode: true, throwOnError: false });
            } catch {
                return `$$${tex}$$`;
            }
        });

        return withDisplay.replace(/\$([^$\n]+?)\$/g, (_, tex) => {
            try {
                return window.katex.renderToString(tex.trim(), { displayMode: false, throwOnError: false });
            } catch {
                return `$${tex}$`;
            }
        });
    }

    function formatTextWithParagraphs(el, rawText) {
        if (!rawText) {
            el.textContent = '—';
            return;
        }
        el.innerHTML = '';
        const paragraphs = rawText.split(/\n\n+/);
        paragraphs.forEach((pText) => {
            const p = document.createElement('p');
            p.style.marginBottom = '10px';
            p.innerHTML = renderMathString(pText.trim());
            el.appendChild(p);
        });
    }

    function initMonaco() {
        document.querySelectorAll('#langSelector .seg-btn').forEach((b) => {
            b.classList.toggle('active', b.dataset.lang === currentLang);
        });

        renderLedgerTable();
        fetchProblems();

        require.config({ paths: { vs: 'https://cdnjs.cloudflare.com/ajax/libs/monaco-editor/0.45.0/min/vs' } });
        require(['vs/editor/editor.main'], () => {
            editor = monaco.editor.create(document.getElementById('editorContainer'), {
                value: '',
                language: monacoLangMap[currentLang],
                theme: 'vs',
                fontFamily: "'JetBrains Mono', monospace",
                fontSize: 13.5,
                lineHeight: 21,
                minimap: { enabled: false },
                scrollBeyondLastLine: false,
                renderLineHighlight: 'all',
                padding: { top: 16, bottom: 16 },
                automaticLayout: true,
                tabSize: 4
            });

            editor.onDidChangeModelContent(() => {
                if (currentProblem && editor) {
                    localStorage.setItem(`sj_code_${currentProblem.id}_${currentLang}`, editor.getValue());
                }
            });

            editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter, () => {
                triggerSubmission();
            });

            loadCodeForCurrentProblem();
        });
    }

    async function fetchProblems() {
        try {
            const resp = await fetch('/api/v1/problems');
            problems = (await resp.json()) || [];
            renderCatalog();
            handleRoute();
        } catch (err) {
            console.error('Failed to load problem catalog:', err);
        }
    }

    async function selectProblem(problemId) {
        if (!problemId) return;
        try {
            const resp = await fetch(`/api/v1/problems/${encodeURIComponent(problemId)}`);
            if (!resp.ok) return;
            currentProblem = await resp.json();
            localStorage.setItem('sj_problem_id', currentProblem.id);

            const statusMap = getProblemStatusMap();
            const solveBadge = document.getElementById('problemSolveBadge');
            if (statusMap.get(currentProblem.id) === 'solved') {
                solveBadge.classList.remove('hidden');
            } else {
                solveBadge.classList.add('hidden');
            }

            document.getElementById('problemDifficulty').textContent = `${currentProblem.category} · ${currentProblem.difficulty}`;
            document.getElementById('problemTitle').textContent = currentProblem.title;

            formatTextWithParagraphs(document.getElementById('problemDescription'), currentProblem.description);
            formatTextWithParagraphs(document.getElementById('problemInputFormat'), currentProblem.input_format);
            formatTextWithParagraphs(document.getElementById('problemOutputFormat'), currentProblem.output_format);

            document.getElementById('testCountPill').textContent = `TESTS: ${currentProblem.test_count || 1}`;
            document.getElementById('timeLimitPill').textContent = `TL: ${currentProblem.time_limit_ms} MS`;
            document.getElementById('memLimitPill').textContent = `ML: ${currentProblem.memory_limit_mb} MB`;

            const constraintsList = document.getElementById('problemConstraints');
            constraintsList.innerHTML = '';
            (currentProblem.constraints || []).forEach((c) => {
                const li = document.createElement('li');
                li.innerHTML = `• ${renderMathString(c)}`;
                constraintsList.appendChild(li);
            });

            const examplesContainer = document.getElementById('problemExamples');
            examplesContainer.innerHTML = '';
            (currentProblem.examples || []).forEach((ex, idx) => {
                const card = document.createElement('div');
                card.className = 'example-card';
                card.innerHTML = `
                    <div class="example-col">
                        <span class="example-label">Sample Input 0${idx + 1}</span>
                        <pre class="example-pre">${escapeHtml(ex.input)}</pre>
                    </div>
                    <div class="example-col">
                        <span class="example-label">Expected Output 0${idx + 1}</span>
                        <pre class="example-pre">${escapeHtml(ex.output)}</pre>
                    </div>
                `;
                examplesContainer.appendChild(card);
            });

            loadCodeForCurrentProblem();
        } catch (err) {
            console.error('Failed to load problem specification:', err);
        }
    }

    function loadCodeForCurrentProblem(forceReset = false) {
        if (!currentProblem || !editor) return;
        const storageKey = `sj_code_${currentProblem.id}_${currentLang}`;
        const savedDraft = !forceReset ? localStorage.getItem(storageKey) : null;
        const starter = (currentProblem.starter_code && currentProblem.starter_code[currentLang]) || '';

        editor.setValue(savedDraft !== null ? savedDraft : starter);
        monaco.editor.setModelLanguage(editor.getModel(), monacoLangMap[currentLang]);
        editor.layout();
    }

    function resetStepper() {
        ['stepQueued', 'stepCompiling', 'stepRunning', 'stepVerdict'].forEach((id) => {
            const el = document.getElementById(id);
            el.classList.remove('active', 'completed');
        });
        document.getElementById('stepRunningLabel').textContent = 'RUNNING TESTS';
        document.getElementById('stepVerdictLabel').textContent = 'VERDICT';
    }

    function updateTelemetryUI(state) {
        const status = state.status;
        resetStepper();

        const sQueued = document.getElementById('stepQueued');
        const sCompiling = document.getElementById('stepCompiling');
        const sRunning = document.getElementById('stepRunning');
        const sVerdict = document.getElementById('stepVerdict');

        if (status === 'PENDING_QUEUE' || status === 'QUEUED' || status === 'PROCESSING') {
            sQueued.classList.add('active');
        } else if (status === 'COMPILING') {
            sQueued.classList.add('completed');
            sCompiling.classList.add('active');
        } else if (status === 'RUNNING') {
            sQueued.classList.add('completed');
            sCompiling.classList.add('completed');
            sRunning.classList.add('active');
            if (state.total_tests > 0) {
                document.getElementById('stepRunningLabel').textContent = `RUNNING (${state.current_test}/${state.total_tests})`;
            }
        } else {
            sQueued.classList.add('completed');
            sCompiling.classList.add('completed');
            if (status !== 'COMPILATION_ERROR') {
                sRunning.classList.add('completed');
            }
            sVerdict.classList.add('completed');
            document.getElementById('stepVerdictLabel').textContent = status;
        }

        const banner = document.getElementById('verdictBanner');
        banner.className = 'verdict-banner';
        if (status === 'ACCEPTED') {
            banner.classList.add('state-ac');
        } else if (status === 'TIME_LIMIT_EXCEEDED' || status === 'MEMORY_LIMIT_EXCEEDED' || status === 'WRONG_ANSWER') {
            banner.classList.add('state-warn');
        } else if (status === 'COMPILATION_ERROR' || status === 'RUNTIME_ERROR' || status === 'INTERNAL_ERROR') {
            banner.classList.add('state-err');
        }

        document.getElementById('verdictBadge').textContent = status;
        document.getElementById('verdictTimestamp').textContent = new Date().toLocaleTimeString();

        if (state.verdict_details) {
            document.getElementById('verdictDetails').textContent = state.verdict_details;
        } else if (status === 'RUNNING') {
            document.getElementById('verdictDetails').textContent = `Running test vector ${state.current_test} of ${state.total_tests}...`;
        } else {
            document.getElementById('verdictDetails').textContent = `Status: ${status}`;
        }

        if (state.execution_time_ms !== undefined && state.execution_time_ms !== null) {
            document.getElementById('metricTime').textContent = `${state.execution_time_ms} MS`;
        }
        if (state.execution_memory_kb !== undefined && state.execution_memory_kb !== null) {
            document.getElementById('metricMemory').textContent = `${state.execution_memory_kb} KB`;
        }
    }

    function isTerminal(status) {
        return [
            'ACCEPTED',
            'WRONG_ANSWER',
            'TIME_LIMIT_EXCEEDED',
            'MEMORY_LIMIT_EXCEEDED',
            'COMPILATION_ERROR',
            'RUNTIME_ERROR',
            'INTERNAL_ERROR'
        ].includes(status);
    }

    async function triggerSubmission() {
        if (!currentProblem || !editor) return;
        const code = editor.getValue();
        if (!code.trim()) return;

        if (activeEventSource) {
            activeEventSource.close();
            activeEventSource = null;
        }

        submitBtn.disabled = true;
        document.getElementById('metricTime').textContent = '—';
        document.getElementById('metricMemory').textContent = '—';

        try {
            const resp = await fetch('/api/v1/submit', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    user_id: sessionUserId,
                    question_id: currentProblem.id,
                    language: currentLang,
                    code: code,
                    time_limit_ms: currentProblem.time_limit_ms,
                    memory_limit_mb: currentProblem.memory_limit_mb
                })
            });

            if (!resp.ok) {
                const errText = await resp.text();
                updateTelemetryUI({
                    status: 'INTERNAL_ERROR',
                    verdict_details: `HTTP ${resp.status}: ${errText}`
                });
                submitBtn.disabled = false;
                return;
            }

            const data = await resp.json();
            const subId = data.submission_id;
            document.getElementById('activeSubmissionId').textContent = subId.slice(0, 8);

            updateTelemetryUI({
                submission_id: subId,
                status: 'QUEUED',
                verdict_details: 'Queued for evaluation.'
            });

            streamSubmissionTelemetry(subId);
        } catch (err) {
            updateTelemetryUI({
                status: 'INTERNAL_ERROR',
                verdict_details: `Network dispatch error: ${err.message}`
            });
            submitBtn.disabled = false;
        }
    }

    function streamSubmissionTelemetry(subId) {
        activeEventSource = new EventSource(`/api/v1/submissions/${subId}/stream`);

        activeEventSource.onmessage = async (event) => {
            try {
                const payload = JSON.parse(event.data);
                updateTelemetryUI(payload);

                if (isTerminal(payload.status)) {
                    activeEventSource.close();
                    activeEventSource = null;
                    submitBtn.disabled = false;

                    const finalResp = await fetch(`/api/v1/submissions/${subId}`);
                    if (finalResp.ok) {
                        const finalRecord = await finalResp.json();
                        updateTelemetryUI(finalRecord);
                        recordInLedger(finalRecord);
                    }
                }
            } catch (e) {
                console.error('Malformed SSE payload:', e);
            }
        };

        activeEventSource.onerror = async () => {
            if (activeEventSource) {
                activeEventSource.close();
                activeEventSource = null;
            }
            submitBtn.disabled = false;
            const fallbackResp = await fetch(`/api/v1/submissions/${subId}`);
            if (fallbackResp.ok) {
                const record = await fallbackResp.json();
                updateTelemetryUI(record);
                if (isTerminal(record.status)) {
                    recordInLedger(record);
                }
            }
        };
    }

    function recordInLedger(record) {
        if (sessionHistory.some((item) => item.submission_id === record.submission_id)) {
            return;
        }
        sessionHistory.unshift(record);
        if (sessionHistory.length > 100) sessionHistory.pop();
        localStorage.setItem('sj_history', JSON.stringify(sessionHistory));
        renderLedgerTable();

        if (currentProblem && record.question_id === currentProblem.id && record.status === 'ACCEPTED') {
            document.getElementById('problemSolveBadge').classList.remove('hidden');
        }
    }

    function renderLedgerTable() {
        document.getElementById('historyCount').textContent = sessionHistory.length;
        const tbody = document.getElementById('historyTableBody');
        if (sessionHistory.length === 0) return;

        tbody.innerHTML = '';
        sessionHistory.forEach((item) => {
            const tr = document.createElement('tr');
            tr.className = 'ledger-row';
            const latency = item.execution_time_ms !== undefined && item.execution_time_ms !== null
                ? `${item.execution_time_ms} ms`
                : '—';
            tr.innerHTML = `
                <td>${item.submission_id.slice(0, 8)}</td>
                <td>${item.question_id}</td>
                <td>${item.language.toUpperCase()}</td>
                <td><strong>${item.status}</strong></td>
                <td>${latency}</td>
            `;
            tr.addEventListener('click', () => {
                updateTelemetryUI(item);
                document.getElementById('activeSubmissionId').textContent = item.submission_id.slice(0, 8);
            });
            tbody.appendChild(tr);
        });
    }

    function escapeHtml(str) {
        return (str || '')
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;');
    }

    window.addEventListener('hashchange', handleRoute);

    navCatalogBtn.addEventListener('click', () => {
        window.location.hash = '#/';
    });

    navWorkspaceBtn.addEventListener('click', () => {
        const targetId = (currentProblem && currentProblem.id) || localStorage.getItem('sj_problem_id') || (problems[0] && problems[0].id);
        if (targetId) {
            window.location.hash = `#/problem/${encodeURIComponent(targetId)}`;
        }
    });

    catalogSearchInput.addEventListener('input', (e) => {
        catalogSearchQuery = e.target.value || '';
        renderCatalog();
    });

    document.querySelectorAll('#statusFilterGroup .seg-btn').forEach((btn) => {
        btn.addEventListener('click', () => {
            document.querySelectorAll('#statusFilterGroup .seg-btn').forEach((b) => b.classList.remove('active'));
            btn.classList.add('active');
            catalogStatusFilter = btn.dataset.filter;
            renderCatalog();
        });
    });

    document.querySelectorAll('#langSelector .seg-btn').forEach((btn) => {
        btn.addEventListener('click', () => {
            document.querySelectorAll('#langSelector .seg-btn').forEach((b) => b.classList.remove('active'));
            btn.classList.add('active');
            currentLang = btn.dataset.lang;
            localStorage.setItem('sj_lang', currentLang);
            loadCodeForCurrentProblem();
        });
    });

    resetCodeBtn.addEventListener('click', () => {
        if (currentProblem) {
            localStorage.removeItem(`sj_code_${currentProblem.id}_${currentLang}`);
        }
        loadCodeForCurrentProblem(true);
    });

    submitBtn.addEventListener('click', triggerSubmission);

    document.querySelectorAll('.tab-btn').forEach((btn) => {
        btn.addEventListener('click', () => {
            document.querySelectorAll('.tab-btn').forEach((b) => b.classList.remove('active'));
            document.querySelectorAll('.tab-panel').forEach((p) => p.classList.remove('active'));
            btn.classList.add('active');
            document.getElementById(btn.dataset.tab).classList.add('active');
        });
    });

    initMonaco();
})();

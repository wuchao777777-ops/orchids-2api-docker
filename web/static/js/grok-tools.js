(() => {
  const imagineState = {
    running: false,
    mode: "auto",
    effectiveMode: "auto",
    taskIDs: [],
    wsSockets: [],
    sseStreams: [],
    imageCount: 0,
    latencySum: 0,
    latencyCount: 0,
    fallbackTimer: null,
  };

  const cacheOnlineState = {
    selectedTokens: new Set(),
    accounts: [],
    details: [],
    online: {},
    onlineScope: "none",
    accountMap: new Map(),
    detailMap: new Map(),
  };

  const cacheBatchState = {
    running: false,
    action: "",
    taskID: "",
    total: 0,
    processed: 0,
    statusText: "空闲",
    eventSource: null,
  };

  const videoState = {
    taskID: "",
    stream: null,
    running: false,
    fileDataURL: "",
    startAt: 0,
    elapsedTimer: null,
    contentBuffer: "",
  };

  const voiceState = {
    running: false,
    room: null,
    localTracks: [],
    visualizerTimer: null,
  };

  const chatState = {
    sessions: [],
    activeId: "",
    sending: false,
    abortController: null,
    pendingFile: null,
    sidebarOpen: false,
    model: "grok-420",
    models: [
      "grok-3",
      "grok-3-mini",
      "grok-3-thinking",
      "grok-4",
      "grok-4-mini",
      "grok-4-thinking",
      "grok-4-heavy",
      "grok-4.1-mini",
      "grok-4.1-fast",
      "grok-4.1-expert",
      "grok-4.1-thinking",
      "grok-420",
    ],
  };
  const chatStorageKey = "grok_tools_chat_sessions_v1";
  const grokToolsUIStorageKey = "grok_tools_ui_v1";

  function handleUnauthorized(res) {
    if (res && res.status === 401) {
      window.location.href = "/admin/login.html?next=" + encodeURIComponent("/admin/?tab=grok-tools");
      return true;
    }
    return false;
  }

  function detectImageMime(b64) {
    const raw = String(b64 || "");
    if (raw.startsWith("iVBOR")) return "image/png";
    if (raw.startsWith("/9j/")) return "image/jpeg";
    if (raw.startsWith("R0lGOD")) return "image/gif";
    return "image/jpeg";
  }

  function formatBytes(bytes) {
    const num = Number(bytes || 0);
    if (!Number.isFinite(num) || num <= 0) return "0 B";
    const units = ["B", "KB", "MB", "GB", "TB"];
    let value = num;
    let idx = 0;
    while (value >= 1024 && idx < units.length - 1) {
      value /= 1024;
      idx++;
    }
    return `${value.toFixed(value >= 10 ? 1 : 2)} ${units[idx]}`;
  }

  function formatTimeMS(ms) {
    const num = Number(ms || 0);
    if (!Number.isFinite(num) || num <= 0) return "-";
    return `${Math.round(num)} ms`;
  }

  function formatDateTime(ms) {
    const num = Number(ms || 0);
    if (!Number.isFinite(num) || num <= 0) return "-";
    return new Date(num).toLocaleString();
  }

  function relativeTime(ms) {
    const num = Number(ms || 0);
    if (!Number.isFinite(num) || num <= 0) return "-";
    const diff = Math.max(0, Math.round((Date.now() - num) / 1000));
    if (diff < 60) return "刚刚";
    if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`;
    if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`;
    return `${Math.floor(diff / 86400)} 天前`;
  }

  function loadGrokToolsUIState() {
    try {
      const raw = localStorage.getItem(grokToolsUIStorageKey);
      if (!raw) return {};
      const parsed = JSON.parse(raw);
      return parsed && typeof parsed === "object" ? parsed : {};
    } catch (err) {
      return {};
    }
  }

  function saveGrokToolsUIState(patch) {
    try {
      const current = loadGrokToolsUIState();
      localStorage.setItem(grokToolsUIStorageKey, JSON.stringify({ ...current, ...patch }));
    } catch (err) {
      // ignore storage failures
    }
  }

  function setChatSendButtonState(sending) {
    const btn = document.getElementById("grokSendBtn");
    if (!btn) return;
    btn.disabled = false;
    if (sending) {
      btn.title = "停止生成";
      btn.innerHTML = `
        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <rect x="6" y="6" width="12" height="12" rx="2"></rect>
        </svg>
      `;
      return;
    }
    btn.title = "发送";
    btn.innerHTML = `
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <path d="M22 2L11 13"></path>
        <path d="M22 2L15 22L11 13L2 9L22 2Z"></path>
      </svg>
    `;
  }

  function closeChatSidebar() {
    chatState.sidebarOpen = false;
    document.getElementById("grokChatSidebar")?.classList.remove("show");
    document.getElementById("grokChatSidebarOverlay")?.classList.remove("show");
  }

  function openChatSidebar() {
    chatState.sidebarOpen = true;
    document.getElementById("grokChatSidebar")?.classList.add("show");
    document.getElementById("grokChatSidebarOverlay")?.classList.add("show");
  }

  function resolveOnlineStatusText(status) {
    const raw = String(status || "").trim();
    if (raw === "ok") return "连接正常";
    if (raw === "not_loaded") return "未加载";
    if (raw === "no_token") return "无可用 Token";
    if (!raw) return "未知";
    return raw;
  }

  function normalizeOnlineToken(raw) {
    const token = String(raw || "").trim();
    if (!token) return "";
    if (!token.includes("sso=")) return token;
    const idx = token.indexOf("sso=");
    const tail = token.slice(idx + 4);
    const semi = tail.indexOf(";");
    return (semi >= 0 ? tail.slice(0, semi) : tail).trim();
  }

  function formatTokenMask(token) {
    const raw = String(token || "").trim();
    if (!raw) return "";
    if (raw.length <= 24) return raw;
    return `${raw.slice(0, 8)}...${raw.slice(-16)}`;
  }

  function toNumberOrZero(value) {
    const n = Number(value || 0);
    return Number.isFinite(n) ? n : 0;
  }

  function closeCacheBatchStream() {
    const es = cacheBatchState.eventSource;
    cacheBatchState.eventSource = null;
    if (!es) return;
    try {
      es.close();
    } catch (err) {
      // ignore
    }
  }

  function updateCacheBatchUI() {
    const statusEl = document.getElementById("cacheOnlineBatchStatus");
    const progressEl = document.getElementById("cacheOnlineBatchProgress");
    const barEl = document.getElementById("cacheOnlineBatchBar");
    const cancelBtn = document.getElementById("cacheOnlineBatchCancelBtn");
    const loadSelectedBtn = document.getElementById("cacheOnlineLoadSelectedBtn");
    const loadAllBtn = document.getElementById("cacheOnlineLoadAllBtn");
    const clearSelectedBtn = document.getElementById("cacheOnlineClearSelectedBtn");

    const total = Math.max(0, Math.floor(toNumberOrZero(cacheBatchState.total)));
    const processed = Math.max(0, Math.floor(toNumberOrZero(cacheBatchState.processed)));
    const safeTotal = total > 0 ? total : 0;
    const safeProcessed = total > 0 ? Math.min(processed, total) : 0;
    const percent = safeTotal > 0 ? Math.floor((safeProcessed / safeTotal) * 100) : 0;

    if (statusEl) {
      const text = String(cacheBatchState.statusText || "").trim();
      statusEl.textContent = text || (cacheBatchState.running ? "运行中" : "空闲");
    }
    if (progressEl) progressEl.textContent = `${safeProcessed}/${safeTotal}`;
    if (barEl) barEl.value = percent;
    if (cancelBtn) {
      cancelBtn.style.display = cacheBatchState.running ? "inline-flex" : "none";
      cancelBtn.disabled = !cacheBatchState.running;
    }
    if (loadSelectedBtn) loadSelectedBtn.disabled = cacheBatchState.running;
    if (loadAllBtn) loadAllBtn.disabled = cacheBatchState.running;
    if (clearSelectedBtn) clearSelectedBtn.disabled = cacheBatchState.running;
  }

  function applyCacheBatchProgress(msg) {
    if (!msg || typeof msg !== "object") return;
    if (typeof msg.total === "number" && Number.isFinite(msg.total)) {
      cacheBatchState.total = Math.max(0, Math.floor(msg.total));
    }
    if (typeof msg.processed === "number" && Number.isFinite(msg.processed)) {
      cacheBatchState.processed = Math.max(0, Math.floor(msg.processed));
    } else if (typeof msg.done === "number" && Number.isFinite(msg.done)) {
      cacheBatchState.processed = Math.max(0, Math.floor(msg.done));
    }
    if (cacheBatchState.total > 0 && cacheBatchState.processed > cacheBatchState.total) {
      cacheBatchState.total = cacheBatchState.processed;
    }
    updateCacheBatchUI();
  }

  function beginCacheBatch(action, taskID, total, statusText) {
    closeCacheBatchStream();
    cacheBatchState.running = true;
    cacheBatchState.action = String(action || "").trim();
    cacheBatchState.taskID = String(taskID || "").trim();
    cacheBatchState.total = Math.max(0, Math.floor(toNumberOrZero(total)));
    cacheBatchState.processed = 0;
    cacheBatchState.statusText = String(statusText || "运行中");
    updateCacheBatchUI();
  }

  function finishCacheBatch(statusText) {
    cacheBatchState.running = false;
    cacheBatchState.action = "";
    cacheBatchState.taskID = "";
    cacheBatchState.statusText = String(statusText || "空闲");
    closeCacheBatchStream();
    updateCacheBatchUI();
  }

  function openCacheBatchStream(taskID, handlers = {}) {
    const cleanTaskID = String(taskID || "").trim();
    if (!cleanTaskID) throw new Error("empty task_id");

    const url = `/api/v1/admin/batch/${encodeURIComponent(cleanTaskID)}/stream?t=${Date.now()}`;
    const es = new EventSource(url);
    cacheBatchState.eventSource = es;
    let ended = false;

    const doneOnce = (fn) => {
      if (ended) return;
      ended = true;
      closeCacheBatchStream();
      if (typeof fn === "function") {
        Promise.resolve()
          .then(() => fn())
          .catch((err) => {
            showToast(err?.message || String(err || "批量任务处理失败"), "error");
          });
      }
    };

    es.onmessage = (event) => {
      let msg = null;
      try {
        msg = JSON.parse(event.data);
      } catch (err) {
        return;
      }
      if (!msg || typeof msg !== "object") return;
      const msgTaskID = String(msg.task_id || "").trim();
      if (msgTaskID && msgTaskID !== cleanTaskID) return;

      applyCacheBatchProgress(msg);
      const type = String(msg.type || "").trim().toLowerCase();
      if (type === "snapshot" || type === "progress") {
        return;
      }
      if (type === "done") {
        doneOnce(() => {
          if (typeof handlers.onDone === "function") {
            handlers.onDone(msg);
          }
        });
        return;
      }
      if (type === "cancelled") {
        doneOnce(() => {
          if (typeof handlers.onCancelled === "function") {
            handlers.onCancelled(msg);
          }
        });
        return;
      }
      if (type === "error") {
        doneOnce(() => {
          if (typeof handlers.onError === "function") {
            handlers.onError(String(msg.error || "unknown error"), msg);
          }
        });
      }
    };

    es.onerror = () => {
      doneOnce(() => {
        if (typeof handlers.onError === "function") {
          handlers.onError("连接中断", null);
        }
      });
    };
  }

  function setImagineStatus(text) {
    const el = document.getElementById("imagineStatus");
    if (el) el.textContent = String(text || "");
  }

  function syncImagineModeUI(mode) {
    const normalized = String(mode || "auto").toLowerCase();
    const select = document.getElementById("imagineMode");
    if (select) select.value = normalized;
    document.querySelectorAll("[data-imagine-mode]").forEach((btn) => {
      const active = String(btn.dataset.imagineMode || "").toLowerCase() === normalized;
      btn.classList.toggle("active", active);
    });
  }

  function setImagineButtons(running) {
    const startBtn = document.getElementById("imagineStartBtn");
    const stopBtn = document.getElementById("imagineStopBtn");
    if (startBtn) startBtn.disabled = !!running;
    if (stopBtn) stopBtn.disabled = !running;
  }

  function imagineOptionEnabled(id, fallback) {
    const input = document.getElementById(id);
    if (!input) return !!fallback;
    return !!input.checked;
  }

  function updateImagineActiveCount() {
    const el = document.getElementById("imagineActive");
    if (!el) return;
    if (imagineState.effectiveMode === "sse") {
      const count = imagineState.sseStreams.filter((s) => s && s.readyState !== 2).length;
      el.textContent = String(count);
      return;
    }
    const count = imagineState.wsSockets.filter((w) => w && w.readyState === 1).length;
    el.textContent = String(count);
  }

  function resetImagineMetrics() {
    imagineState.imageCount = 0;
    imagineState.latencySum = 0;
    imagineState.latencyCount = 0;
    const count = document.getElementById("imagineCount");
    const latency = document.getElementById("imagineLatency");
    if (count) count.textContent = "0";
    if (latency) latency.textContent = "-";
  }

  function appendImagineImage(b64, seq, elapsedMS, fileURL) {
    const grid = document.getElementById("imagineGrid");
    const empty = document.getElementById("imagineEmpty");
    if (!grid) return;
    if (empty) empty.style.display = "none";

    const card = document.createElement("div");
    card.className = "imagine-card";

    const img = document.createElement("img");
    let src = "";
    if (fileURL) {
      src = fileURL;
    } else if (b64) {
      const mime = detectImageMime(b64);
      src = `data:${mime};base64,${b64}`;
    }
    if (!src) return;
    img.src = src;
    img.alt = `imagine-${seq || 0}`;
    img.loading = "lazy";
    const openURL = fileURL || src;
    img.addEventListener("click", () => window.open(openURL, "_blank", "noopener"));

    if (imagineOptionEnabled("imagineAutoFilter", false) && b64) {
      const raw = String(b64 || "").replace(/\s/g, "");
      let padding = 0;
      if (raw.endsWith("==")) padding = 2;
      else if (raw.endsWith("=")) padding = 1;
      const estimatedBytes = Math.max(0, Math.floor((raw.length * 3) / 4) - padding);
      if (estimatedBytes > 0 && estimatedBytes < 100000) {
        return;
      }
    }

    const meta = document.createElement("div");
    meta.className = "imagine-meta";
    const left = document.createElement("span");
    left.textContent = `#${seq || 0}`;
    const right = document.createElement("span");
    right.textContent = formatTimeMS(elapsedMS);
    meta.appendChild(left);
    meta.appendChild(right);

    card.appendChild(img);
    card.appendChild(meta);
    if (imagineOptionEnabled("imagineReverseInsert", true)) {
      grid.prepend(card);
    } else {
      grid.appendChild(card);
    }
    if (imagineOptionEnabled("imagineAutoDownload", false)) {
      const link = document.createElement("a");
      link.href = openURL;
      link.download = `grok-imagine-${seq || Date.now()}.${fileURL && /\.png(\?|$)/i.test(fileURL) ? "png" : fileURL && /\.webp(\?|$)/i.test(fileURL) ? "webp" : "jpg"}`;
      document.body.appendChild(link);
      link.click();
      link.remove();
    }
    if (imagineOptionEnabled("imagineAutoScroll", true)) {
      card.scrollIntoView({ behavior: "smooth", block: "nearest" });
    }
  }

  function handleImagineMessage(payload) {
    if (!payload || typeof payload !== "object") return;
    if (payload.type === "image") {
      const b64 = String(payload.b64_json || "");
      const fileURL = String(payload.file_url || payload.url || "");
      if (!b64 && !fileURL) return;
      imagineState.imageCount += 1;
      const count = document.getElementById("imagineCount");
      if (count) count.textContent = String(imagineState.imageCount);

      const elapsed = Number(payload.elapsed_ms || 0);
      if (elapsed > 0) {
        imagineState.latencySum += elapsed;
        imagineState.latencyCount += 1;
        const avg = Math.round(imagineState.latencySum / imagineState.latencyCount);
        const latency = document.getElementById("imagineLatency");
        if (latency) latency.textContent = `${avg} ms`;
      }
      appendImagineImage(b64, payload.sequence, payload.elapsed_ms, fileURL);
      return;
    }

    if (payload.type === "status") {
      if (payload.status === "running") {
        setImagineStatus(`运行中 (${imagineState.effectiveMode.toUpperCase()})`);
      } else if (payload.status === "stopped") {
        if (imagineState.running) {
          setImagineStatus("已停止");
        }
      }
      return;
    }

    if (payload.type === "error") {
      const msg = String(payload.message || "Imagine 运行出错");
      showToast(msg, "error");
      setImagineStatus("错误");
    }
  }

  function closeImagineConnections(sendStop) {
    if (imagineState.fallbackTimer) {
      clearTimeout(imagineState.fallbackTimer);
      imagineState.fallbackTimer = null;
    }

    imagineState.wsSockets.forEach((ws) => {
      if (!ws) return;
      if (sendStop && ws.readyState === 1) {
        try {
          ws.send(JSON.stringify({ type: "stop" }));
        } catch (err) {
          // ignore
        }
      }
      try {
        ws.close(1000, "stop");
      } catch (err) {
        // ignore
      }
    });
    imagineState.wsSockets = [];

    imagineState.sseStreams.forEach((es) => {
      if (!es) return;
      try {
        es.close();
      } catch (err) {
        // ignore
      }
    });
    imagineState.sseStreams = [];
    updateImagineActiveCount();
  }

  async function createImagineTask(prompt, aspectRatio) {
    const res = await fetch("/api/v1/admin/imagine/start", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        prompt,
        aspect_ratio: aspectRatio,
        nsfw: true,
      }),
    });
    if (handleUnauthorized(res)) {
      throw new Error("unauthorized");
    }
    if (!res.ok) {
      throw new Error(await res.text());
    }
    const data = await res.json();
    return String((data && data.task_id) || "").trim();
  }

  async function stopImagineTasks(taskIDs) {
    if (!Array.isArray(taskIDs) || taskIDs.length === 0) return;
    const res = await fetch("/api/v1/admin/imagine/stop", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ task_ids: taskIDs }),
    });
    if (handleUnauthorized(res)) return;
  }

  function startImagineSSE(taskIDs) {
    imagineState.effectiveMode = "sse";
    setImagineStatus("连接中 (SSE)");
    closeImagineConnections(false);

    taskIDs.forEach((taskID, idx) => {
      const url = `/api/v1/admin/imagine/sse?task_id=${encodeURIComponent(taskID)}&conn=${idx}&t=${Date.now()}`;
      const es = new EventSource(url);
      es.onopen = () => {
        setImagineStatus("运行中 (SSE)");
        updateImagineActiveCount();
      };
      es.onmessage = (event) => {
        try {
          handleImagineMessage(JSON.parse(event.data));
        } catch (err) {
          // ignore bad payload
        }
      };
      es.onerror = () => {
        updateImagineActiveCount();
        const alive = imagineState.sseStreams.filter((s) => s && s.readyState !== 2).length;
        if (alive === 0 && imagineState.running) {
          setImagineStatus("连接异常");
        }
      };
      imagineState.sseStreams.push(es);
    });
  }

  function startImagineWS(taskIDs, prompt, aspectRatio, allowFallback) {
    imagineState.effectiveMode = "ws";
    setImagineStatus("连接中 (WS)");
    closeImagineConnections(false);

    let opened = 0;
    let switched = false;

    if (allowFallback) {
      imagineState.fallbackTimer = setTimeout(() => {
        if (!imagineState.running || opened > 0 || switched) return;
        switched = true;
        showToast("WS 建连失败，自动切换 SSE", "info");
        startImagineSSE(taskIDs);
      }, 1500);
    }

    taskIDs.forEach((taskID) => {
      const protocol = window.location.protocol === "https:" ? "wss" : "ws";
      const url = `${protocol}://${window.location.host}/api/v1/admin/imagine/ws?task_id=${encodeURIComponent(taskID)}`;
      const ws = new WebSocket(url);

      ws.onopen = () => {
        opened += 1;
        updateImagineActiveCount();
        setImagineStatus("运行中 (WS)");
        try {
          ws.send(JSON.stringify({
            type: "start",
            prompt,
            aspect_ratio: aspectRatio,
          }));
        } catch (err) {
          // ignore
        }
      };

      ws.onmessage = (event) => {
        try {
          handleImagineMessage(JSON.parse(event.data));
        } catch (err) {
          // ignore bad payload
        }
      };

      ws.onerror = () => {
        if (allowFallback && opened === 0 && !switched) {
          switched = true;
          startImagineSSE(taskIDs);
          return;
        }
        updateImagineActiveCount();
      };

      ws.onclose = () => {
        updateImagineActiveCount();
      };

      imagineState.wsSockets.push(ws);
    });
  }

  async function startImagine() {
    if (imagineState.running) {
      showToast("Imagine 已在运行中", "info");
      return;
    }
    const prompt = String(document.getElementById("imaginePrompt")?.value || "").trim();
    if (!prompt) {
      showToast("请输入 Prompt", "error");
      return;
    }
    const ratio = String(document.getElementById("imagineRatio")?.value || "2:3");
    const concurrent = Math.max(1, Math.min(3, Number(document.getElementById("imagineConcurrent")?.value || 1)));
    const mode = String(document.getElementById("imagineMode")?.value || "auto").toLowerCase();

    imagineState.running = true;
    imagineState.mode = mode;
    setImagineButtons(true);
    setImagineStatus("创建任务中");

    const taskIDs = [];
    try {
      for (let i = 0; i < concurrent; i++) {
        const taskID = await createImagineTask(prompt, ratio);
        if (!taskID) {
          throw new Error("创建任务失败：空 task_id");
        }
        taskIDs.push(taskID);
      }

      imagineState.taskIDs = taskIDs;
      if (mode === "sse") {
        startImagineSSE(taskIDs);
      } else if (mode === "ws") {
        startImagineWS(taskIDs, prompt, ratio, false);
      } else {
        startImagineWS(taskIDs, prompt, ratio, true);
      }
      showToast(`Imagine 已启动 (${taskIDs.length} 并发)`, "success");
    } catch (err) {
      imagineState.running = false;
      setImagineButtons(false);
      setImagineStatus("启动失败");
      await stopImagineTasks(taskIDs);
      imagineState.taskIDs = [];
      showToast(`启动失败: ${err.message || err}`, "error");
    }
  }

  async function stopImagine() {
    const taskIDs = imagineState.taskIDs.slice();
    imagineState.running = false;
    setImagineButtons(false);
    setImagineStatus("停止中");
    closeImagineConnections(true);
    imagineState.taskIDs = [];
    try {
      await stopImagineTasks(taskIDs);
    } catch (err) {
      // ignore
    }
    updateImagineActiveCount();
    setImagineStatus("已停止");
  }

  function clearImagineGrid() {
    const grid = document.getElementById("imagineGrid");
    const empty = document.getElementById("imagineEmpty");
    if (grid) grid.innerHTML = "";
    if (empty) empty.style.display = "block";
    resetImagineMetrics();
  }

  function createChatSession() {
    return {
      id: `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 8)}`,
      title: "新会话",
      isDefaultTitle: true,
      createdAt: Date.now(),
      updatedAt: Date.now(),
      messages: [],
      model: chatState.model,
    };
  }

  function isImageFileName(name) {
    return /\.(png|jpe?g|webp|gif|bmp|svg)$/i.test(String(name || "").trim());
  }

  function readFileAsDataURL(file) {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result || ""));
      reader.onerror = () => reject(reader.error || new Error("读取文件失败"));
      reader.readAsDataURL(file);
    });
  }

  function saveChatSessions() {
    try {
      const sessions = chatState.sessions.map((session) => ({
        ...session,
        messages: Array.isArray(session.messages)
          ? session.messages.map((msg) => ({
              ...msg,
              attachment: msg?.attachment
                ? {
                    name: String(msg.attachment.name || ""),
                    type: String(msg.attachment.type || ""),
                  }
                : undefined,
            }))
          : [],
      }));
      localStorage.setItem(chatStorageKey, JSON.stringify({
        activeId: chatState.activeId,
        model: chatState.model,
        sessions,
      }));
    } catch (err) {
      // ignore storage failures
    }
  }

  function loadChatSessions() {
    try {
      const raw = localStorage.getItem(chatStorageKey);
      if (raw) {
        const parsed = JSON.parse(raw);
        if (parsed && Array.isArray(parsed.sessions)) {
          chatState.sessions = parsed.sessions;
          chatState.activeId = String(parsed.activeId || "");
          if (typeof parsed.model === "string" && parsed.model.trim()) {
            chatState.model = parsed.model.trim();
          }
        }
      }
    } catch (err) {
      chatState.sessions = [];
      chatState.activeId = "";
    }
    if (!Array.isArray(chatState.sessions) || chatState.sessions.length === 0) {
      const session = createChatSession();
      chatState.sessions = [session];
      chatState.activeId = session.id;
    }
    if (!chatState.sessions.find((item) => item && item.id === chatState.activeId)) {
      chatState.activeId = chatState.sessions[0].id;
    }
    chatState.sessions.forEach((session) => {
      if (session && typeof session.isDefaultTitle === "undefined") {
        session.isDefaultTitle = !session.title || session.title === "新会话";
      }
    });
  }

  function activeChatSession() {
    return chatState.sessions.find((item) => item && item.id === chatState.activeId) || null;
  }

  function updateChatStatus(text, type) {
    const el = document.getElementById("grokChatStatus");
    if (!el) return;
    el.textContent = String(text || "");
    el.classList.remove("connected", "connecting", "error");
    if (type === "ok") el.classList.add("connected");
    if (type === "connecting") el.classList.add("connecting");
    if (type === "error") el.classList.add("error");
  }

  function escapeHTML(text) {
    const div = document.createElement("div");
    div.textContent = String(text == null ? "" : text);
    return div.innerHTML;
  }

  function renderAssistantContent(raw) {
    const safe = escapeHTML(raw);
    let html = safe.replace(/```([\s\S]*?)```/g, (_m, code) => `<pre>${code}</pre>`);
    html = html.replace(/`([^`]+)`/g, "<code>$1</code>");
    html = html.replace(/\n/g, "<br>");
    return html;
  }

  function renderUserContent(content, attachment) {
    const wrapper = document.createElement("div");
    const text = document.createElement("div");
    text.className = "message-content";
    text.textContent = String(content || "");
    wrapper.appendChild(text);
    if (attachment && attachment.name) {
      const badge = document.createElement("div");
      badge.className = "message-attachment";
      badge.textContent = `附件: ${attachment.name}`;
      wrapper.appendChild(badge);
    }
    return wrapper;
  }

  function appendChatMessage(role, content, attachment) {
    const log = document.getElementById("grokChatLog");
    const empty = document.getElementById("grokChatEmpty");
    if (!log) return null;
    if (empty) empty.style.display = "none";
    const row = document.createElement("div");
    row.className = `message-row ${role}`;
    const bubble = document.createElement("div");
    bubble.className = "message-bubble";
    let contentEl = document.createElement("div");
    contentEl.className = role === "assistant" ? "message-content rendered" : "message-content";
    if (role === "assistant") {
      contentEl.innerHTML = renderAssistantContent(content || "");
      bubble.appendChild(contentEl);
    } else {
      contentEl = renderUserContent(content, attachment);
      bubble.appendChild(contentEl);
    }
    row.appendChild(bubble);
    log.appendChild(row);
    log.scrollTop = log.scrollHeight;
    return contentEl;
  }

  function rerenderChatThread() {
    const log = document.getElementById("grokChatLog");
    const empty = document.getElementById("grokChatEmpty");
    if (!log) return;
    log.innerHTML = "";
    const session = activeChatSession();
    const messages = Array.isArray(session?.messages) ? session.messages : [];
    if (messages.length === 0) {
      if (empty) {
        empty.style.display = "block";
        log.appendChild(empty);
      }
      return;
    }
    messages.forEach((msg) => appendChatMessage(msg.role, msg.content, msg.attachment));
  }

  function renderChatSessions() {
    const list = document.getElementById("grokSessionList");
    if (!list) return;
    list.innerHTML = "";
    chatState.sessions.forEach((session) => {
      const item = document.createElement("button");
      item.type = "button";
      item.className = `session-item${session.id === chatState.activeId ? " active" : ""}`;
      item.dataset.id = session.id;

      const title = document.createElement("span");
      title.className = "session-title";
      title.textContent = session.title || "新会话";
      title.addEventListener("dblclick", (event) => {
        event.stopPropagation();
        startRenameChatSession(session.id, title);
      });

      const meta = document.createElement("span");
      meta.className = "session-meta";
      meta.textContent = relativeTime(session.updatedAt);

      const delBtn = document.createElement("button");
      delBtn.type = "button";
      delBtn.className = "session-delete";
      delBtn.title = "删除";
      delBtn.textContent = "×";
      delBtn.addEventListener("click", (event) => {
        event.stopPropagation();
        deleteChatSession(session.id);
      });

      item.addEventListener("click", () => {
        switchChatSession(session.id);
        closeChatSidebar();
      });

      item.appendChild(title);
      item.appendChild(meta);
      item.appendChild(delBtn);
      list.appendChild(item);
    });
  }

  function syncChatModelUI() {
    const label = document.getElementById("grokModelLabel");
    if (label) label.textContent = chatState.model;
    const session = activeChatSession();
    if (session) {
      session.model = chatState.model;
    }
  }

  function renderChatModelDropdown() {
    const dropdown = document.getElementById("grokModelDropdown");
    if (!dropdown) return;
    dropdown.innerHTML = "";
    chatState.models.forEach((model) => {
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = `model-option${model === chatState.model ? " active" : ""}`;
      btn.textContent = model;
      btn.dataset.model = model;
      dropdown.appendChild(btn);
    });
  }

  async function loadChatModels() {
    try {
      const res = await fetch("/grok/v1/models");
      if (handleUnauthorized(res)) return;
      if (!res.ok) return;
      const data = await res.json();
      const models = Array.isArray(data?.data)
        ? data.data
            .map((item) => String(item?.id || "").trim())
            .filter((id) => id && !id.includes("imagine"))
        : [];
      if (models.length === 0) return;
      chatState.models = models;
      if (!models.includes(chatState.model)) {
        chatState.model = models.includes("grok-420")
          ? "grok-420"
          : models.includes("grok-4")
            ? "grok-4"
            : models[0];
      }
    } catch (err) {
      // ignore model fetch failures and keep fallback list
    }
  }

  function ensureChatTitle(session) {
    if (!session || !Array.isArray(session.messages) || session.messages.length === 0) return;
    if (session.isDefaultTitle === false) return;
    const firstUser = session.messages.find((msg) => msg && msg.role === "user" && String(msg.content || "").trim());
    if (!firstUser) return;
    session.title = String(firstUser.content || "").replace(/\s+/g, " ").trim().slice(0, 20) || "新会话";
    session.isDefaultTitle = false;
  }

  function renameChatSession(id, newTitle) {
    const session = chatState.sessions.find((item) => item && item.id === id);
    if (!session) return;
    const trimmed = String(newTitle || "").trim();
    session.title = trimmed || "新会话";
    session.isDefaultTitle = !trimmed;
    session.updatedAt = Date.now();
    saveChatSessions();
    renderChatSessions();
  }

  function deleteChatSession(id) {
    const idx = chatState.sessions.findIndex((item) => item && item.id === id);
    if (idx < 0) return;
    chatState.sessions.splice(idx, 1);
    if (chatState.sessions.length === 0) {
      const session = createChatSession();
      chatState.sessions = [session];
      chatState.activeId = session.id;
    } else if (chatState.activeId === id) {
      chatState.activeId = chatState.sessions[Math.max(0, idx - 1)].id;
    }
    renderChatSessions();
    rerenderChatThread();
    saveChatSessions();
  }

  function startRenameChatSession(sessionId, titleEl) {
    const session = chatState.sessions.find((item) => item && item.id === sessionId);
    if (!session || !titleEl || !titleEl.parentNode) return;
    const input = document.createElement("input");
    input.type = "text";
    input.className = "session-rename-input";
    input.value = String(session.title || "");
    input.maxLength = 40;
    titleEl.replaceWith(input);
    input.focus();
    input.select();
    const commit = () => renameChatSession(sessionId, input.value);
    input.addEventListener("blur", commit);
    input.addEventListener("keydown", (event) => {
      if (event.key === "Enter") {
        event.preventDefault();
        input.blur();
      }
      if (event.key === "Escape") {
        input.value = session.title || "新会话";
        input.blur();
      }
    });
  }

  function switchChatSession(id) {
    if (!id || id === chatState.activeId) return;
    chatState.activeId = id;
    const session = activeChatSession();
    if (session && session.model) {
      chatState.model = session.model;
    }
    syncChatModelUI();
    renderChatModelDropdown();
    renderChatSessions();
    rerenderChatThread();
    saveChatSessions();
  }

  function newChatSession() {
    const session = createChatSession();
    chatState.sessions.unshift(session);
    chatState.activeId = session.id;
    syncChatModelUI();
    renderChatModelDropdown();
    renderChatSessions();
    rerenderChatThread();
    saveChatSessions();
    updateChatStatus("就绪");
  }

  function buildChatPayload() {
    const session = activeChatSession();
    if (!session) {
      throw new Error("missing chat session");
    }
    const systemPrompt = String(document.getElementById("grokSystemInput")?.value || "").trim();
    const messages = [];
    if (systemPrompt) {
      messages.push({ role: "system", content: systemPrompt });
    }
    session.messages.forEach((msg) => {
      if (msg.role !== "user" || !msg.attachment?.dataUrl) {
        messages.push({ role: msg.role, content: msg.content });
        return;
      }
      const parts = [{ type: "text", text: String(msg.content || "") }];
      if (isImageFileName(msg.attachment.name)) {
        parts.push({
          type: "image_url",
          image_url: { url: msg.attachment.dataUrl },
        });
      } else {
        parts.push({
          type: "file",
          file: {
            filename: String(msg.attachment.name || "upload.bin"),
            file_data: msg.attachment.dataUrl,
          },
        });
      }
      messages.push({ role: msg.role, content: parts });
    });
    return {
      model: chatState.model,
      stream: true,
      temperature: Number(document.getElementById("grokTempRange")?.value || 0.8),
      top_p: Number(document.getElementById("grokTopPRange")?.value || 0.95),
      messages,
    };
  }

  async function sendChatMessage() {
    if (chatState.sending) return;
    const input = document.getElementById("grokPromptInput");
    const prompt = String(input?.value || "").trim();
    if (!prompt) return;
    const attachment = chatState.pendingFile ? { ...chatState.pendingFile } : null;
    const session = activeChatSession();
    if (!session) return;
    session.messages.push({ role: "user", content: prompt, attachment });
    session.updatedAt = Date.now();
    ensureChatTitle(session);
    renderChatSessions();
    rerenderChatThread();
    if (input) {
      input.value = "";
      input.style.height = "40px";
    }

    clearPendingChatFile();
    const bubble = appendChatMessage("assistant", "");
    let assistantText = "";
    chatState.sending = true;
    chatState.abortController = new AbortController();
    setChatSendButtonState(true);
    updateChatStatus("连接中...", "connecting");

    try {
      const payload = buildChatPayload();
      const res = await fetch("/grok/v1/chat/completions", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
        signal: chatState.abortController.signal,
      });
      if (handleUnauthorized(res)) return;
      if (!res.ok || !res.body) {
        throw new Error(await res.text());
      }
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      while (true) {
        const { value, done } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        let idx = buffer.indexOf("\n\n");
        while (idx >= 0) {
          const chunk = buffer.slice(0, idx);
          buffer = buffer.slice(idx + 2);
          const lines = chunk.split("\n");
          let data = "";
          lines.forEach((line) => {
            if (line.startsWith("data:")) {
              data += line.slice(5).trimStart();
            }
          });
          if (!data) {
            idx = buffer.indexOf("\n\n");
            continue;
          }
          if (data === "[DONE]") {
            idx = buffer.indexOf("\n\n");
            continue;
          }
          let payloadChunk = null;
          try {
            payloadChunk = JSON.parse(data);
          } catch (err) {
            idx = buffer.indexOf("\n\n");
            continue;
          }
          const choice = payloadChunk?.choices?.[0];
          const delta = typeof choice?.delta?.content === "string" ? choice.delta.content : "";
          const finalContent = typeof choice?.message?.content === "string" ? choice.message.content : "";
          if (delta) {
            assistantText += delta;
          } else if (finalContent) {
            assistantText = finalContent;
          }
          if (bubble) {
            bubble.innerHTML = renderAssistantContent(assistantText);
            const log = document.getElementById("grokChatLog");
            if (log) log.scrollTop = log.scrollHeight;
          }
          idx = buffer.indexOf("\n\n");
        }
      }
      session.messages.push({ role: "assistant", content: assistantText.trim() });
      session.updatedAt = Date.now();
      saveChatSessions();
      updateChatStatus("完成", "ok");
    } catch (err) {
      if (err && err.name === "AbortError") {
        if (bubble && !assistantText.trim()) {
          bubble.innerHTML = renderAssistantContent("[stopped]");
        }
        updateChatStatus("已停止", "error");
        return;
      }
      if (bubble) {
        bubble.innerHTML = renderAssistantContent(`[error] ${err.message || err}`);
      }
      updateChatStatus(err.message || "发送失败", "error");
    } finally {
      chatState.sending = false;
      chatState.abortController = null;
      setChatSendButtonState(false);
      renderChatSessions();
      saveChatSessions();
    }
  }

  function bindChatEvents() {
    const newBtn = document.getElementById("grokChatNewBtn");
    const list = document.getElementById("grokSessionList");
    const sendBtn = document.getElementById("grokSendBtn");
    const input = document.getElementById("grokPromptInput");
    const modelChip = document.getElementById("grokModelChip");
    const modelDropdown = document.getElementById("grokModelDropdown");
    const settingsToggle = document.getElementById("grokSettingsToggle");
    const settingsPanel = document.getElementById("grokSettingsPanel");
    const attachBtn = document.getElementById("grokAttachBtn");
    const fileInput = document.getElementById("grokFileInput");
    const fileRemoveBtn = document.getElementById("grokFileRemoveBtn");
    const sidebarToggle = document.getElementById("grokChatSidebarToggle");
    const sidebarOverlay = document.getElementById("grokChatSidebarOverlay");
    const collapseBtn = document.getElementById("grokChatCollapseBtn");
    const expandBtn = document.getElementById("grokChatExpandBtn");
    const tempRange = document.getElementById("grokTempRange");
    const tempValue = document.getElementById("grokTempValue");
    const topPRange = document.getElementById("grokTopPRange");
    const topPValue = document.getElementById("grokTopPValue");

    if (newBtn) newBtn.addEventListener("click", () => {
      newChatSession();
      closeChatSidebar();
    });
    if (sendBtn) {
      sendBtn.addEventListener("click", () => {
        if (chatState.sending && chatState.abortController) {
          chatState.abortController.abort();
          return;
        }
        sendChatMessage().catch(() => {});
      });
    }
    if (attachBtn && fileInput) {
      attachBtn.addEventListener("click", () => fileInput.click());
    }
    if (fileInput) {
      fileInput.addEventListener("change", async () => {
        const file = fileInput.files && fileInput.files[0];
        if (!file) return;
        try {
          const dataUrl = await readFileAsDataURL(file);
          chatState.pendingFile = {
            name: file.name || "upload.bin",
            type: file.type || "",
            dataUrl,
          };
          renderPendingChatFile();
        } catch (err) {
          showToast(err?.message || "读取文件失败", "error");
        } finally {
          fileInput.value = "";
        }
      });
    }
    if (fileRemoveBtn) {
      fileRemoveBtn.addEventListener("click", () => clearPendingChatFile());
    }
    if (input) {
      input.addEventListener("input", () => {
        input.style.height = "40px";
        input.style.height = `${Math.min(input.scrollHeight, 160)}px`;
      });
      input.addEventListener("keydown", (event) => {
        if ((event.ctrlKey || event.metaKey) && event.key === "Enter") {
          event.preventDefault();
          sendChatMessage().catch(() => {});
        }
      });
    }
    if (sidebarToggle) sidebarToggle.addEventListener("click", () => openChatSidebar());
    if (expandBtn) expandBtn.addEventListener("click", () => openChatSidebar());
    if (collapseBtn) collapseBtn.addEventListener("click", () => closeChatSidebar());
    if (sidebarOverlay) sidebarOverlay.addEventListener("click", () => closeChatSidebar());
    if (modelChip && modelDropdown) {
      modelChip.addEventListener("click", (event) => {
        event.stopPropagation();
        modelDropdown.classList.toggle("show");
      });
      modelDropdown.addEventListener("click", (event) => {
        const btn = event.target.closest(".model-option");
        if (!btn || !modelDropdown.contains(btn)) return;
        chatState.model = String(btn.dataset.model || chatState.model);
        syncChatModelUI();
        renderChatModelDropdown();
        saveChatSessions();
        modelDropdown.classList.remove("show");
      });
    }
    if (settingsToggle && settingsPanel) {
      settingsToggle.addEventListener("click", (event) => {
        event.stopPropagation();
        settingsPanel.classList.toggle("show");
      });
    }
    document.addEventListener("click", () => {
      modelDropdown?.classList.remove("show");
      settingsPanel?.classList.remove("show");
    });
    if (tempRange && tempValue) {
      tempRange.addEventListener("input", () => {
        tempValue.textContent = String(Number(tempRange.value).toFixed(2)).replace(/\.00$/, "");
        saveGrokToolsUIState({ chatTemperature: Number(tempRange.value) });
      });
    }
    if (topPRange && topPValue) {
      topPRange.addEventListener("input", () => {
        topPValue.textContent = String(Number(topPRange.value).toFixed(2)).replace(/\.00$/, "");
        saveGrokToolsUIState({ chatTopP: Number(topPRange.value) });
      });
    }
    const systemInput = document.getElementById("grokSystemInput");
    if (systemInput) {
      systemInput.addEventListener("input", () => {
        saveGrokToolsUIState({ chatSystemPrompt: String(systemInput.value || "") });
      });
    }
  }

  async function initChat() {
    await loadChatModels();
    loadChatSessions();
    const uiState = loadGrokToolsUIState();
    if (!chatState.models.includes(chatState.model)) {
      chatState.model = chatState.models.includes("grok-420")
        ? "grok-420"
        : chatState.models.includes("grok-4")
          ? "grok-4"
          : (chatState.models[0] || chatState.model);
    }
    const tempRange = document.getElementById("grokTempRange");
    const tempValue = document.getElementById("grokTempValue");
    const topPRange = document.getElementById("grokTopPRange");
    const topPValue = document.getElementById("grokTopPValue");
    const systemInput = document.getElementById("grokSystemInput");
    if (tempRange && typeof uiState.chatTemperature === "number") {
      tempRange.value = String(uiState.chatTemperature);
    }
    if (tempValue && tempRange) {
      tempValue.textContent = String(Number(tempRange.value).toFixed(2)).replace(/\.00$/, "");
    }
    if (topPRange && typeof uiState.chatTopP === "number") {
      topPRange.value = String(uiState.chatTopP);
    }
    if (topPValue && topPRange) {
      topPValue.textContent = String(Number(topPRange.value).toFixed(2)).replace(/\.00$/, "");
    }
    if (systemInput && typeof uiState.chatSystemPrompt === "string") {
      systemInput.value = uiState.chatSystemPrompt;
    }
    syncChatModelUI();
    renderChatModelDropdown();
    renderChatSessions();
    rerenderChatThread();
    renderPendingChatFile();
    setChatSendButtonState(false);
    bindChatEvents();
    closeChatSidebar();
  }

  function renderPendingChatFile() {
    const badge = document.getElementById("grokFileBadge");
    const name = document.getElementById("grokFileName");
    if (!badge || !name) return;
    if (!chatState.pendingFile?.name) {
      badge.classList.add("hidden");
      name.textContent = "";
      return;
    }
    badge.classList.remove("hidden");
    name.textContent = chatState.pendingFile.name;
  }

  function clearPendingChatFile() {
    chatState.pendingFile = null;
    renderPendingChatFile();
  }

  function setVoiceStatus(text, type) {
    const el = document.getElementById("voiceStatus");
    if (!el) return;
    el.textContent = String(text || "");
    el.style.color = type === "error" ? "var(--accent-red)" : type === "ok" ? "var(--accent-green)" : "var(--text-primary)";
  }

  function appendVoiceLog(line) {
    const el = document.getElementById("voiceLogOutput");
    if (!el) return;
    const time = new Date().toLocaleTimeString();
    el.textContent += `[${time}] ${String(line || "")}\n`;
    el.scrollTop = el.scrollHeight;
  }

  function clearVoiceLog() {
    const el = document.getElementById("voiceLogOutput");
    if (el) el.textContent = "";
  }

  function setVoiceButtons(running) {
    const start = document.getElementById("voiceStartBtn");
    const stop = document.getElementById("voiceStopBtn");
    if (start) start.disabled = !!running;
    if (stop) stop.disabled = !running;
  }

  function updateVoiceMeta() {
    const voice = String(document.getElementById("voiceName")?.value || "ara").trim() || "ara";
    const personality = String(document.getElementById("voicePersonality")?.value || "assistant").trim() || "assistant";
    const speed = Math.max(0.1, Number(document.getElementById("voiceSpeed")?.value || 1));
    const statusVoice = document.getElementById("voiceStatusVoice");
    const statusPersonality = document.getElementById("voiceStatusPersonality");
    const statusSpeed = document.getElementById("voiceStatusSpeed");
    const speedValue = document.getElementById("voiceSpeedValue");
    if (statusVoice) statusVoice.textContent = voice;
    if (statusPersonality) statusPersonality.textContent = personality;
    if (statusSpeed) statusSpeed.textContent = `${speed}x`;
    if (speedValue) speedValue.textContent = speed.toFixed(1);
  }

  function initVoiceVisualizer() {
    const root = document.getElementById("voiceVisualizer");
    if (!root || root.childElementCount > 0) return;
    for (let i = 0; i < 18; i += 1) {
      const bar = document.createElement("span");
      bar.style.flex = "1";
      bar.style.background = "linear-gradient(180deg,var(--accent-cyan),var(--accent-green))";
      bar.style.borderRadius = "999px";
      bar.style.height = "8%";
      root.appendChild(bar);
    }
  }

  function stopVoiceVisualizer() {
    if (voiceState.visualizerTimer) {
      clearInterval(voiceState.visualizerTimer);
      voiceState.visualizerTimer = null;
    }
    const root = document.getElementById("voiceVisualizer");
    if (!root) return;
    Array.from(root.children).forEach((bar) => {
      bar.style.height = "8%";
    });
  }

  function startVoiceVisualizer() {
    initVoiceVisualizer();
    stopVoiceVisualizer();
    const root = document.getElementById("voiceVisualizer");
    if (!root) return;
    voiceState.visualizerTimer = window.setInterval(() => {
      Array.from(root.children).forEach((bar) => {
        bar.style.height = `${10 + Math.floor(Math.random() * 80)}%`;
      });
    }, 180);
  }

  function resetVoiceAudio() {
    const root = document.getElementById("voiceAudioRoot");
    if (root) root.innerHTML = "";
  }

  async function fetchVoiceToken() {
    updateVoiceMeta();
    const voice = String(document.getElementById("voiceName")?.value || "ara").trim() || "ara";
    const personality = String(document.getElementById("voicePersonality")?.value || "assistant").trim() || "assistant";
    const speed = Number(document.getElementById("voiceSpeed")?.value || 1);
    const url = `/api/v1/admin/voice/token?voice=${encodeURIComponent(voice)}&personality=${encodeURIComponent(personality)}&speed=${encodeURIComponent(speed > 0 ? speed : 1)}`;

    const res = await fetch(url);
    if (handleUnauthorized(res)) return null;
    if (!res.ok) {
      throw new Error(await res.text());
    }
    const data = await res.json();
    const token = String(data.token || "");
    const livekitURL = String(data.url || "");
    const tokenOutput = document.getElementById("voiceTokenOutput");
    const urlOutput = document.getElementById("voiceUrlOutput");
    const urlStatus = document.getElementById("voiceStatusURL");
    if (tokenOutput) tokenOutput.value = token;
    if (urlOutput) urlOutput.value = livekitURL;
    if (urlStatus) urlStatus.textContent = livekitURL || "-";
    appendVoiceLog("Fetched voice token");
    showToast("Voice Token 获取成功", "success");
    return { token, url: livekitURL };
  }

  async function stopVoiceSession() {
    if (voiceState.room && typeof voiceState.room.disconnect === "function") {
      try {
        await voiceState.room.disconnect();
      } catch (err) {
        // ignore
      }
    }
    voiceState.room = null;
    voiceState.localTracks = [];
    voiceState.running = false;
    setVoiceButtons(false);
    stopVoiceVisualizer();
    resetVoiceAudio();
    setVoiceStatus("已停止");
    appendVoiceLog("Voice session stopped");
  }

  async function startVoiceSession() {
    if (voiceState.running) {
      showToast("Voice 会话已在运行中", "info");
      return;
    }
    if (!window.LiveKitClient) {
      throw new Error("LiveKit SDK 未加载");
    }
    setVoiceButtons(true);
    setVoiceStatus("连接中...");
    updateVoiceMeta();
    const payload = await fetchVoiceToken();
    if (!payload || !payload.token || !payload.url) {
      throw new Error("voice token unavailable");
    }

    const room = new window.LiveKitClient.Room();
    voiceState.room = room;
    voiceState.running = true;
    room.on(window.LiveKitClient.RoomEvent.TrackSubscribed, (track) => {
      if (!track || track.kind !== "audio") return;
      const root = document.getElementById("voiceAudioRoot");
      if (!root) return;
      root.innerHTML = "";
      try {
        const el = track.attach();
        el.autoplay = true;
        el.controls = true;
        root.appendChild(el);
      } catch (err) {
        appendVoiceLog(`Attach audio failed: ${err.message || err}`);
      }
    });
    room.on(window.LiveKitClient.RoomEvent.Disconnected, () => {
      stopVoiceSession().catch(() => {});
    });

    try {
      await room.connect(payload.url, payload.token);
      await room.localParticipant.setMicrophoneEnabled(true);
      setVoiceStatus("已连接", "ok");
      appendVoiceLog("Voice session connected");
      startVoiceVisualizer();
    } catch (err) {
      await stopVoiceSession();
      setVoiceStatus(err.message || "连接失败", "error");
      throw err;
    }
  }

  function setVideoStatus(text, type) {
    const el = document.getElementById("videoStatus");
    if (!el) return;
    el.textContent = String(text || "");
    el.style.color = type === "error" ? "var(--accent-red)" : type === "ok" ? "var(--accent-green)" : "var(--text-primary)";
  }

  function appendVideoLog(line) {
    const el = document.getElementById("videoLogOutput");
    if (!el) return;
    el.textContent += `${String(line || "")}\n`;
    el.scrollTop = el.scrollHeight;
  }

  function setVideoButtons(running) {
    const start = document.getElementById("videoStartBtn");
    const stop = document.getElementById("videoStopBtn");
    if (start) start.disabled = !!running;
    if (stop) stop.disabled = !running;
  }

  function setVideoProgress(value) {
    const safe = Math.max(0, Math.min(100, Number(value) || 0));
    const fill = document.getElementById("videoProgressFill");
    const text = document.getElementById("videoProgressText");
    if (fill) fill.style.width = `${safe}%`;
    if (text) text.textContent = `${safe}%`;
  }

  function stopVideoElapsedTimer() {
    if (videoState.elapsedTimer) {
      clearInterval(videoState.elapsedTimer);
      videoState.elapsedTimer = null;
    }
  }

  function startVideoElapsedTimer() {
    stopVideoElapsedTimer();
    const duration = document.getElementById("videoDurationValue");
    videoState.elapsedTimer = window.setInterval(() => {
      if (!videoState.startAt || !duration) return;
      const seconds = Math.max(0, Math.round((Date.now() - videoState.startAt) / 1000));
      duration.textContent = `${seconds}s`;
    }, 1000);
  }

  function clearVideoOutput() {
    const stage = document.getElementById("videoStage");
    const empty = document.getElementById("videoEmpty");
    const log = document.getElementById("videoLogOutput");
    if (stage) stage.innerHTML = "";
    if (empty) empty.style.display = "block";
    if (log) log.textContent = "";
    setVideoProgress(0);
    const duration = document.getElementById("videoDurationValue");
    if (duration) duration.textContent = "-";
    videoState.contentBuffer = "";
  }

  function renderVideoPreview(url) {
    const stage = document.getElementById("videoStage");
    const empty = document.getElementById("videoEmpty");
    if (!stage) return;
    if (empty) empty.style.display = "none";
    stage.innerHTML = "";
    const wrap = document.createElement("div");
    wrap.style.display = "grid";
    wrap.style.gap = "12px";
    const video = document.createElement("video");
    video.controls = true;
    video.preload = "metadata";
    video.src = url;
    video.style.width = "100%";
    video.style.borderRadius = "14px";
    video.style.background = "#000";
    const actions = document.createElement("div");
    actions.style.display = "flex";
    actions.style.gap = "8px";
    const open = document.createElement("a");
    open.className = "btn btn-outline";
    open.href = url;
    open.target = "_blank";
    open.rel = "noopener";
    open.textContent = "打开";
    const download = document.createElement("a");
    download.className = "btn btn-outline";
    download.href = url;
    download.download = "";
    download.textContent = "下载";
    actions.appendChild(open);
    actions.appendChild(download);
    wrap.appendChild(video);
    wrap.appendChild(actions);
    stage.appendChild(wrap);
  }

  function extractVideoURL(raw) {
    const text = String(raw || "");
    const patterns = [
      /<source[^>]*src=["']([^"']+)["']/i,
      /<video[^>]*src=["']([^"']+)["']/i,
      /\[video\]\(([^)]+)\)/i,
      /(https?:\/\/[^\s"'`)<]+)/i,
      /(\/grok\/v1\/files\/video\/[^\s"'`)<]+)/i,
      /(\/v1\/files\/video\/[^\s"'`)<]+)/i,
    ];
    const sanitize = (candidate) => {
      const input = String(candidate || "").trim().replace(/[),.;]+$/g, "");
      if (!input) return "";
      const extMatch = input.match(/\.(mp4|webm)(?:[?#][^\s"'`)<]*)?/i);
      if (!extMatch || extMatch.index == null) return "";
      const end = extMatch.index + extMatch[0].length;
      return input.slice(0, end);
    };
    for (const pattern of patterns) {
      const match = text.match(pattern);
      if (match && match[1]) {
        const clean = sanitize(match[1]);
        if (clean) return clean;
      }
    }
    return "";
  }

  function extractVideoProgress(raw) {
    const text = String(raw || "");
    const match = text.match(/(\d{1,3})\s*%/);
    if (!match || !match[1]) return null;
    const value = Number(match[1]);
    return Number.isFinite(value) ? Math.max(0, Math.min(100, value)) : null;
  }

  async function createVideoTask(payload) {
    const res = await fetch("/api/v1/admin/video/start", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    if (handleUnauthorized(res)) return "";
    if (!res.ok) {
      throw new Error(await res.text());
    }
    const data = await res.json();
    return String(data.task_id || "").trim();
  }

  async function stopVideoTask() {
    if (!videoState.taskID) return;
    const res = await fetch("/api/v1/admin/video/stop", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ task_ids: [videoState.taskID] }),
    });
    if (handleUnauthorized(res)) return;
  }

  function closeVideoStream() {
    if (!videoState.stream) return;
    try {
      videoState.stream.close();
    } catch (err) {
      // ignore
    }
    videoState.stream = null;
  }

  function openVideoSSE(taskID) {
    const url = `/api/v1/admin/video/sse?task_id=${encodeURIComponent(taskID)}&t=${Date.now()}`;
    const es = new EventSource(url);
    videoState.stream = es;
    es.onmessage = (event) => {
      const text = String(event.data || "");
      if (!text) return;
      appendVideoLog(text);
      if (text === "[DONE]") {
        closeVideoStream();
        stopVideoElapsedTimer();
        setVideoButtons(false);
        setVideoStatus("已完成", "ok");
        setVideoProgress(100);
        return;
      }
      let payload = null;
      try {
        payload = JSON.parse(text);
      } catch (err) {
        payload = null;
      }
      const errorMsg = payload && (payload.error || payload?.error?.message);
      if (errorMsg) {
        closeVideoStream();
        stopVideoElapsedTimer();
        setVideoButtons(false);
        setVideoStatus(String(errorMsg), "error");
        return;
      }
      const choice = payload?.choices?.[0];
      const deltaContent = typeof choice?.delta?.content === "string" ? choice.delta.content : "";
      const messageContent = typeof choice?.message?.content === "string" ? choice.message.content : "";
      const content = deltaContent || messageContent || text;
      videoState.contentBuffer += content;
      const progress = extractVideoProgress(content);
      if (progress !== null) {
        setVideoProgress(progress);
      }
      const videoURL = extractVideoURL(content) || extractVideoURL(videoState.contentBuffer);
      if (videoURL) {
        renderVideoPreview(videoURL);
        setVideoProgress(100);
      }
    };
    es.onerror = () => {
      closeVideoStream();
      stopVideoElapsedTimer();
      setVideoButtons(false);
      if (videoState.running) {
        setVideoStatus("连接中断", "error");
      }
    };
  }

  async function startVideo() {
    if (videoState.running) {
      showToast("Video 任务已在运行中", "info");
      return;
    }
    const prompt = String(document.getElementById("videoPrompt")?.value || "").trim();
    if (!prompt) {
      showToast("请输入 Video Prompt", "error");
      return;
    }
    const payload = {
      prompt,
      aspect_ratio: String(document.getElementById("videoRatio")?.value || "3:2"),
      video_length: Number(document.getElementById("videoLength")?.value || 6),
      resolution_name: String(document.getElementById("videoResolution")?.value || "480p"),
      preset: String(document.getElementById("videoPreset")?.value || "normal"),
      image_url: videoState.fileDataURL || String(document.getElementById("videoImageUrl")?.value || "").trim(),
      reasoning_effort: String(document.getElementById("videoEffort")?.value || "").trim(),
    };
    const aspectValue = document.getElementById("videoAspectValue");
    const lengthValue = document.getElementById("videoLengthValue");
    const resolutionValue = document.getElementById("videoResolutionValue");
    const presetValue = document.getElementById("videoPresetValue");
    if (aspectValue) aspectValue.textContent = payload.aspect_ratio || "-";
    if (lengthValue) lengthValue.textContent = `${payload.video_length || "-"}s`;
    if (resolutionValue) resolutionValue.textContent = payload.resolution_name || "-";
    if (presetValue) presetValue.textContent = payload.preset || "-";
    clearVideoOutput();
    setVideoButtons(true);
    setVideoStatus("创建任务中...");
    videoState.running = true;
    try {
      const taskID = await createVideoTask(payload);
      if (!taskID) {
        throw new Error("创建任务失败：空 task_id");
      }
      videoState.taskID = taskID;
      videoState.startAt = Date.now();
      startVideoElapsedTimer();
      setVideoStatus("运行中", "ok");
      openVideoSSE(taskID);
    } catch (err) {
      videoState.running = false;
      setVideoButtons(false);
      setVideoStatus(err.message || "启动失败", "error");
      throw err;
    }
  }

  async function stopVideo() {
    videoState.running = false;
    closeVideoStream();
    stopVideoElapsedTimer();
    setVideoButtons(false);
    setVideoStatus("已停止");
    try {
      await stopVideoTask();
    } catch (err) {
      // ignore
    }
    videoState.taskID = "";
  }

  function normalizeOnlineAccounts(rawAccounts) {
    const list = Array.isArray(rawAccounts) ? rawAccounts : [];
    const out = [];
    for (const item of list) {
      const token = normalizeOnlineToken(item?.token);
      if (!token) continue;
      out.push({
        ...item,
        token,
        token_masked: String(item?.token_masked || formatTokenMask(token)),
      });
    }
    return out;
  }

  function normalizeOnlineDetails(rawDetails) {
    const list = Array.isArray(rawDetails) ? rawDetails : [];
    const out = [];
    for (const item of list) {
      const token = normalizeOnlineToken(item?.token);
      if (!token) continue;
      out.push({
        ...item,
        token,
        token_masked: String(item?.token_masked || formatTokenMask(token)),
      });
    }
    return out;
  }

  function currentOnlineRows() {
    const rows = [];
    const online = cacheOnlineState.online || {};
    const detailsMap = cacheOnlineState.detailMap;
    if (cacheOnlineState.accounts.length > 0) {
      for (const acc of cacheOnlineState.accounts) {
        const token = normalizeOnlineToken(acc.token);
        if (!token) continue;
        const detail = detailsMap.get(token);
        const isOnlineToken = normalizeOnlineToken(online.token) === token;
        const count = detail ? toNumberOrZero(detail.count) : (isOnlineToken ? toNumberOrZero(online.count) : null);
        const status = String(detail?.status || (isOnlineToken ? online.status : "not_loaded") || "not_loaded");
        const lastClear = detail?.last_asset_clear_at ?? (isOnlineToken ? online.last_asset_clear_at : acc.last_asset_clear_at);
        rows.push({
          token,
          token_masked: String(acc.token_masked || detail?.token_masked || formatTokenMask(token)),
          pool: String(acc.pool || "-"),
          count,
          status,
          last_asset_clear_at: lastClear,
        });
      }
      return rows;
    }
    for (const detail of cacheOnlineState.details) {
      rows.push({
        token: detail.token,
        token_masked: String(detail.token_masked || formatTokenMask(detail.token)),
        pool: "-",
        count: toNumberOrZero(detail.count),
        status: String(detail.status || "not_loaded"),
        last_asset_clear_at: detail.last_asset_clear_at,
      });
    }
    return rows;
  }

  function syncCacheOnlineSelectAll() {
    const selectAll = document.getElementById("cacheOnlineSelectAll");
    const body = document.getElementById("cacheOnlineBody");
    if (!selectAll || !body) return;
    const checkboxes = Array.from(body.querySelectorAll("input.cache-online-check"));
    if (checkboxes.length === 0) {
      selectAll.checked = false;
      selectAll.indeterminate = false;
      return;
    }
    const selected = checkboxes.filter((item) => item.checked).length;
    selectAll.checked = selected > 0 && selected === checkboxes.length;
    selectAll.indeterminate = selected > 0 && selected < checkboxes.length;
  }

  function renderCacheOnlineTable() {
    const body = document.getElementById("cacheOnlineBody");
    if (!body) return;
    const rows = currentOnlineRows();
    if (rows.length === 0) {
      body.innerHTML = `<tr><td colspan="7" style="text-align:center;color:var(--text-secondary);padding:24px;">暂无在线账号</td></tr>`;
      syncCacheOnlineSelectAll();
      return;
    }

    body.innerHTML = rows.map((row) => {
      const checked = cacheOnlineState.selectedTokens.has(row.token) ? "checked" : "";
      const countText = row.count === null ? "-" : String(row.count);
      const statusText = resolveOnlineStatusText(row.status);
      const lastClear = formatDateTime(row.last_asset_clear_at);
      return `
        <tr>
          <td style="text-align:center;">
            <input type="checkbox" class="cache-online-check" data-token="${encodeURIComponent(row.token)}" ${checked} />
          </td>
          <td><code>${row.token_masked || formatTokenMask(row.token)}</code></td>
          <td><span class="tag">${row.pool || "-"}</span></td>
          <td>${countText}</td>
          <td>${statusText}</td>
          <td>${lastClear}</td>
          <td>
            <button class="btn btn-danger-outline cache-online-clear-btn" data-token="${encodeURIComponent(row.token)}" style="padding:4px 8px;">清理</button>
          </td>
        </tr>
      `;
    }).join("");
    syncCacheOnlineSelectAll();
  }

  function applyCacheOnlineData(data) {
    const online = (data && typeof data === "object") ? (data.online || {}) : {};
    const onlineScope = String(data?.online_scope || "none");
    const accounts = normalizeOnlineAccounts(data?.online_accounts);
    const details = normalizeOnlineDetails(data?.online_details);

    cacheOnlineState.accounts = accounts;
    cacheOnlineState.details = details;
    cacheOnlineState.online = online;
    cacheOnlineState.onlineScope = onlineScope;
    cacheOnlineState.accountMap = new Map();
    cacheOnlineState.detailMap = new Map();
    accounts.forEach((item) => cacheOnlineState.accountMap.set(item.token, item));
    details.forEach((item) => cacheOnlineState.detailMap.set(item.token, item));

    const available = new Set();
    accounts.forEach((item) => available.add(item.token));
    details.forEach((item) => available.add(item.token));
    Array.from(cacheOnlineState.selectedTokens).forEach((token) => {
      if (!available.has(token)) {
        cacheOnlineState.selectedTokens.delete(token);
      }
    });

    const onlineCountEl = document.getElementById("cacheOnlineCount");
    const onlineStatusEl = document.getElementById("cacheOnlineStatus");
    const onlineScopeEl = document.getElementById("cacheOnlineScope");
    const onlineLastClearEl = document.getElementById("cacheOnlineLastClear");
    if (onlineCountEl) onlineCountEl.textContent = String(toNumberOrZero(online.count));
    if (onlineStatusEl) onlineStatusEl.textContent = resolveOnlineStatusText(online.status);
    if (onlineScopeEl) onlineScopeEl.textContent = onlineScope;
    if (onlineLastClearEl) onlineLastClearEl.textContent = formatDateTime(online.last_asset_clear_at);

    renderCacheOnlineTable();
  }

  async function loadCacheSummary(options = {}) {
    const params = new URLSearchParams();
    const tokens = Array.isArray(options.tokens) ? options.tokens.map(normalizeOnlineToken).filter(Boolean) : [];
    const scope = String(options.scope || "").trim().toLowerCase();
    const token = normalizeOnlineToken(options.token);
    if (tokens.length > 0) {
      params.set("tokens", tokens.join(","));
    } else if (scope === "all") {
      params.set("scope", "all");
    } else if (token) {
      params.set("token", token);
    }

    const url = params.toString() ? `/api/v1/admin/cache?${params.toString()}` : "/api/v1/admin/cache";
    const res = await fetch(url);
    if (handleUnauthorized(res)) return;
    if (!res.ok) {
      throw new Error(await res.text());
    }
    const data = await res.json();
    const imageText = `${data?.image?.count || 0} / ${formatBytes(data?.image?.bytes || 0)}`;
    const videoText = `${data?.video?.count || 0} / ${formatBytes(data?.video?.bytes || 0)}`;
    const totalText = `${data?.total?.count || 0} / ${formatBytes(data?.total?.bytes || 0)}`;

    const imageEl = document.getElementById("cacheImageSummary");
    const videoEl = document.getElementById("cacheVideoSummary");
    const totalEl = document.getElementById("cacheTotalSummary");
    const baseEl = document.getElementById("cacheBaseDir");
    if (imageEl) imageEl.textContent = imageText;
    if (videoEl) videoEl.textContent = videoText;
    if (totalEl) totalEl.textContent = totalText;
    if (baseEl) baseEl.textContent = String(data.base_dir || "-");

    applyCacheOnlineData(data);
    return data;
  }

  function renderCacheList(items) {
    const body = document.getElementById("cacheListBody");
    if (!body) return;
    const list = Array.isArray(items) ? items : [];
    if (list.length === 0) {
      body.innerHTML = `<tr><td colspan="5" style="text-align:center;color:var(--text-secondary);padding:24px;">暂无缓存数据</td></tr>`;
      return;
    }

    body.innerHTML = list.map((item) => {
      const mediaType = String(item.media_type || "");
      const name = String(item.name || "");
      const url = String(item.view_url || item.url || "");
      const size = formatBytes(item.size_bytes || item.size || 0);
      const updatedAt = formatDateTime(item.mtime_ms || item.updated_at || 0);
      return `
        <tr>
          <td><span class="tag">${mediaType}</span></td>
          <td><a href="${url}" target="_blank" rel="noopener"><code>${name}</code></a></td>
          <td>${size}</td>
          <td>${updatedAt}</td>
          <td>
            <button class="btn btn-danger-outline cache-delete-btn" data-media-type="${encodeURIComponent(mediaType)}" data-name="${encodeURIComponent(name)}" style="padding:4px 8px;">删除</button>
          </td>
        </tr>
      `;
    }).join("");
  }

  function selectedOnlineTokens() {
    return Array.from(cacheOnlineState.selectedTokens);
  }

  async function cancelCacheBatchTask() {
    const taskID = String(cacheBatchState.taskID || "").trim();
    if (!cacheBatchState.running || !taskID) return;
    const res = await fetch(`/api/v1/admin/batch/${encodeURIComponent(taskID)}/cancel`, {
      method: "POST",
    });
    if (handleUnauthorized(res)) return;
    if (!res.ok) {
      throw new Error(await res.text());
    }
    showToast("已发送取消请求", "info");
  }

  async function startOnlineLoadBatch(payload, label) {
    if (cacheBatchState.running) {
      showToast("有任务正在运行，请稍候", "info");
      return;
    }
    const res = await fetch("/api/v1/admin/cache/online/load/async", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload || {}),
    });
    if (handleUnauthorized(res)) return;
    let data = {};
    try {
      data = await res.json();
    } catch (err) {
      // ignore
    }
    if (!res.ok || String(data.status || "") !== "success") {
      throw new Error(data.detail || data.error || (await res.text()) || "请求失败");
    }

    const taskID = String(data.task_id || "").trim();
    if (!taskID) {
      throw new Error("创建任务失败：空 task_id");
    }
    const total = toNumberOrZero(data.total);
    beginCacheBatch("load", taskID, total, "在线统计加载中");
    showToast(`开始加载 ${label || "账号"} (${total})`, "info");

    openCacheBatchStream(taskID, {
      onDone: async (msg) => {
        try {
          const result = (msg && typeof msg.result === "object") ? msg.result : null;
          if (result) {
            applyCacheOnlineData(result);
          } else {
            await loadCacheSummary(payload || {});
          }

          let ok = 0;
          let fail = 0;
          const details = Array.isArray(result?.online_details) ? result.online_details : [];
          if (details.length > 0) {
            details.forEach((item) => {
              const status = String(item?.status || "").trim().toLowerCase();
              if (status === "ok") ok++;
              else fail++;
            });
          } else {
            const totalDone = Math.max(toNumberOrZero(msg?.total), toNumberOrZero(data.total));
            ok = totalDone;
          }

          cacheBatchState.processed = Math.max(toNumberOrZero(msg?.total), toNumberOrZero(data.total));
          cacheBatchState.total = cacheBatchState.processed;
          finishCacheBatch("空闲");
          showToast(`在线统计加载完成：成功 ${ok}，失败 ${fail}`, fail > 0 ? "info" : "success");
        } catch (err) {
          finishCacheBatch("失败");
          throw err;
        }
      },
      onCancelled: () => {
        finishCacheBatch("已取消");
        showToast("已终止加载", "info");
      },
      onError: (message) => {
        finishCacheBatch("失败");
        showToast(`加载失败: ${message || "未知错误"}`, "error");
      },
    });
  }

  async function startOnlineClearBatch(tokens) {
    const cleanTokens = Array.isArray(tokens) ? tokens.map(normalizeOnlineToken).filter(Boolean) : [];
    if (cleanTokens.length === 0) {
      showToast("请先选择在线账号", "info");
      return;
    }
    if (cacheBatchState.running) {
      showToast("有任务正在运行，请稍候", "info");
      return;
    }

    const res = await fetch("/api/v1/admin/cache/online/clear/async", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ tokens: cleanTokens }),
    });
    if (handleUnauthorized(res)) return;
    let data = {};
    try {
      data = await res.json();
    } catch (err) {
      // ignore
    }
    if (!res.ok || String(data.status || "") !== "success") {
      throw new Error(data.detail || data.error || (await res.text()) || "请求失败");
    }

    const taskID = String(data.task_id || "").trim();
    if (!taskID) {
      throw new Error("创建任务失败：空 task_id");
    }
    const total = toNumberOrZero(data.total);
    beginCacheBatch("clear", taskID, total, "在线资产清理中");
    showToast(`开始清理 ${cleanTokens.length} 个账号`, "info");

    openCacheBatchStream(taskID, {
      onDone: async (msg) => {
        try {
          const result = (msg && typeof msg.result === "object") ? msg.result : {};
          const summary = (result && typeof result.summary === "object") ? result.summary : {};
          const ok = toNumberOrZero(summary.ok);
          const fail = toNumberOrZero(summary.fail);
          const doneTotal = Math.max(toNumberOrZero(summary.total), toNumberOrZero(msg?.total), cleanTokens.length);
          cacheBatchState.processed = doneTotal;
          cacheBatchState.total = doneTotal;
          finishCacheBatch("空闲");
          showToast(`在线清理完成：成功 ${ok}，失败 ${fail}`, fail > 0 ? "info" : "success");
          await loadCacheSummary({ tokens: cleanTokens });
        } catch (err) {
          finishCacheBatch("失败");
          throw err;
        }
      },
      onCancelled: () => {
        finishCacheBatch("已取消");
        showToast("已终止清理", "info");
      },
      onError: (message) => {
        finishCacheBatch("失败");
        showToast(`清理失败: ${message || "未知错误"}`, "error");
      },
    });
  }

  async function clearOnlineAssets(tokens) {
    const cleanTokens = Array.isArray(tokens) ? tokens.map(normalizeOnlineToken).filter(Boolean) : [];
    if (cleanTokens.length === 0) {
      throw new Error("no tokens selected");
    }
    const body = cleanTokens.length === 1 ? { token: cleanTokens[0] } : { tokens: cleanTokens };
    const res = await fetch("/api/v1/admin/cache/online/clear", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (handleUnauthorized(res)) return null;
    if (!res.ok) {
      throw new Error(await res.text());
    }
    return res.json();
  }

  function summarizeOnlineClear(data) {
    if (!data || typeof data !== "object") {
      return { total: 0, success: 0, failed: 0 };
    }
    const result = data.result || {};
    let total = toNumberOrZero(result.total);
    let success = toNumberOrZero(result.success);
    let failed = toNumberOrZero(result.failed);
    if (total > 0 || success > 0 || failed > 0) {
      return { total, success, failed };
    }
    const results = data.results || {};
    Object.values(results).forEach((item) => {
      const sub = item?.result || {};
      total += toNumberOrZero(sub.total);
      success += toNumberOrZero(sub.success);
      failed += toNumberOrZero(sub.failed);
    });
    return { total, success, failed };
  }

  async function loadCacheList() {
    const filter = String(document.getElementById("cacheTypeFilter")?.value || "").trim();
    const fetchByType = async (mediaType) => {
      const url = `/api/v1/admin/cache/list?media_type=${encodeURIComponent(mediaType)}`;
      const res = await fetch(url);
      if (handleUnauthorized(res)) return null;
      if (!res.ok) {
        throw new Error(await res.text());
      }
      const data = await res.json();
      return Array.isArray(data?.items) ? data.items : [];
    };

    if (filter) {
      const items = await fetchByType(filter);
      if (items === null) return;
      renderCacheList(items);
      return;
    }

    const [imageItems, videoItems] = await Promise.all([fetchByType("image"), fetchByType("video")]);
    if (imageItems === null || videoItems === null) return;
    const merged = imageItems.concat(videoItems);
    merged.sort((a, b) => {
      const left = toNumberOrZero(a?.mtime_ms ?? a?.updated_at);
      const right = toNumberOrZero(b?.mtime_ms ?? b?.updated_at);
      return right - left;
    });
    renderCacheList(merged);
  }

  async function refreshCacheView(options = {}) {
    await loadCacheSummary(options);
    await loadCacheList();
  }

  async function loadSelectedOnlineStats() {
    const tokens = selectedOnlineTokens();
    if (tokens.length === 0) {
      showToast("请先选择在线账号", "info");
      return;
    }
    await startOnlineLoadBatch({ tokens }, "选中账号");
  }

  async function loadAllOnlineStats() {
    const allTokens = cacheOnlineState.accounts.map((item) => normalizeOnlineToken(item.token)).filter(Boolean);
    if (allTokens.length === 0) {
      showToast("暂无在线账号", "info");
      return;
    }
    await startOnlineLoadBatch({ scope: "all" }, "全部账号");
  }

  async function clearSelectedOnlineAssets() {
    const tokens = selectedOnlineTokens();
    if (tokens.length === 0) {
      showToast("请先选择在线账号", "info");
      return;
    }
    if (cacheBatchState.running) {
      showToast("有任务正在运行，请稍候", "info");
      return;
    }
    if (!window.confirm(`确认清理选中的 ${tokens.length} 个账号在线资产？`)) return;
    await startOnlineClearBatch(tokens);
  }

  async function clearSingleOnlineAssets(token) {
    if (cacheBatchState.running) {
      showToast("有任务正在运行，请稍候", "info");
      return;
    }
    const cleanToken = normalizeOnlineToken(token);
    if (!cleanToken) return;
    const display = cacheOnlineState.accountMap.get(cleanToken)?.token_masked || formatTokenMask(cleanToken);
    if (!window.confirm(`确认清理账号 ${display} 的在线资产？`)) return;
    const data = await clearOnlineAssets([cleanToken]);
    if (!data) return;
    const summary = summarizeOnlineClear(data);
    showToast(`在线清理完成：成功 ${summary.success}，失败 ${summary.failed}`, "success");
    await loadCacheSummary({ token: cleanToken });
  }

  async function clearCache() {
    const filter = String(document.getElementById("cacheTypeFilter")?.value || "").trim();
    const confirmText = filter ? `确认清空 ${filter} 缓存？` : "确认清空全部缓存？";
    if (!window.confirm(confirmText)) return;

    const requestClear = async (mediaType) => {
      const res = await fetch("/api/v1/admin/cache/clear", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ media_type: mediaType }),
      });
      if (handleUnauthorized(res)) return null;
      if (!res.ok) {
        throw new Error(await res.text());
      }
      return res.json();
    };

    if (filter) {
      const single = await requestClear(filter);
      if (single === null) return;
    } else {
      const result = await Promise.all([requestClear("image"), requestClear("video")]);
      if (result[0] === null || result[1] === null) return;
    }
    showToast("缓存已清空", "success");
    await refreshCacheView();
  }

  async function deleteCacheItem(mediaType, name) {
    const res = await fetch("/api/v1/admin/cache/item/delete", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        media_type: mediaType,
        name,
      }),
    });
    if (handleUnauthorized(res)) return;
    if (!res.ok) {
      throw new Error(await res.text());
    }
    await refreshCacheView();
  }

  function switchGrokToolTab(tab) {
    closeChatSidebar();
    const sections = {
      chat: document.getElementById("grokChatSection"),
      imagine: document.getElementById("grokImagineSection"),
      video: document.getElementById("grokVideoSection"),
      voice: document.getElementById("grokVoiceSection"),
      cache: document.getElementById("grokCacheSection"),
    };
    Object.keys(sections).forEach((key) => {
      const section = sections[key];
      if (!section) return;
      if (key !== tab) {
        section.style.display = "none";
        return;
      }
      section.style.display = "block";
    });

    const tabs = document.querySelectorAll("#grokToolsTabs .tab-item");
    tabs.forEach((btn) => {
      const active = String(btn.dataset.tab || "").toLowerCase() === String(tab || "").toLowerCase();
      btn.classList.toggle("active", active);
    });
    saveGrokToolsUIState({ activeToolTab: String(tab || "chat") });
  }

  window.switchGrokToolTab = switchGrokToolTab;

  function bindEvents() {
    document.querySelectorAll("[data-imagine-mode]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const mode = String(btn.dataset.imagineMode || "auto").toLowerCase();
        syncImagineModeUI(mode);
      });
    });
    const startBtn = document.getElementById("imagineStartBtn");
    const stopBtn = document.getElementById("imagineStopBtn");
    const clearBtn = document.getElementById("imagineClearBtn");
    if (startBtn) startBtn.addEventListener("click", () => startImagine());
    if (stopBtn) stopBtn.addEventListener("click", () => stopImagine());
    if (clearBtn) clearBtn.addEventListener("click", () => clearImagineGrid());

    const videoStartBtn = document.getElementById("videoStartBtn");
    if (videoStartBtn) {
      videoStartBtn.addEventListener("click", async () => {
        try {
          await startVideo();
        } catch (err) {
          showToast(`启动失败: ${err.message || err}`, "error");
        }
      });
    }
    const videoStopBtn = document.getElementById("videoStopBtn");
    if (videoStopBtn) {
      videoStopBtn.addEventListener("click", async () => {
        try {
          await stopVideo();
        } catch (err) {
          showToast(`停止失败: ${err.message || err}`, "error");
        }
      });
    }
    const videoClearBtn = document.getElementById("videoClearBtn");
    if (videoClearBtn) {
      videoClearBtn.addEventListener("click", () => {
        clearVideoOutput();
        setVideoStatus("未启动");
      });
    }
    const videoSelectImageBtn = document.getElementById("videoSelectImageBtn");
    const videoImageFileInput = document.getElementById("videoImageFileInput");
    const videoClearImageBtn = document.getElementById("videoClearImageBtn");
    const videoImageUrl = document.getElementById("videoImageUrl");
    if (videoSelectImageBtn && videoImageFileInput) {
      videoSelectImageBtn.addEventListener("click", () => videoImageFileInput.click());
      videoImageFileInput.addEventListener("change", () => {
        const file = videoImageFileInput.files && videoImageFileInput.files[0];
        const fileName = document.getElementById("videoImageFileName");
        if (!file) {
          videoState.fileDataURL = "";
          if (fileName) fileName.textContent = "未选择文件";
          return;
        }
        if (videoImageUrl) videoImageUrl.value = "";
        if (fileName) fileName.textContent = file.name;
        const reader = new FileReader();
        reader.onload = () => {
          videoState.fileDataURL = typeof reader.result === "string" ? reader.result : "";
        };
        reader.onerror = () => {
          videoState.fileDataURL = "";
          showToast("读取参考图失败", "error");
        };
        reader.readAsDataURL(file);
      });
    }
    if (videoClearImageBtn) {
      videoClearImageBtn.addEventListener("click", () => {
        videoState.fileDataURL = "";
        if (videoImageFileInput) videoImageFileInput.value = "";
        const fileName = document.getElementById("videoImageFileName");
        if (fileName) fileName.textContent = "未选择文件";
      });
    }
    if (videoImageUrl) {
      videoImageUrl.addEventListener("input", () => {
        if (!videoImageUrl.value.trim()) return;
        videoState.fileDataURL = "";
        if (videoImageFileInput) videoImageFileInput.value = "";
        const fileName = document.getElementById("videoImageFileName");
        if (fileName) fileName.textContent = "未选择文件";
      });
    }

    const voiceFetchBtn = document.getElementById("voiceFetchBtn");
    if (voiceFetchBtn) {
      voiceFetchBtn.addEventListener("click", async () => {
        try {
          await fetchVoiceToken();
        } catch (err) {
          showToast(`获取失败: ${err.message || err}`, "error");
        }
      });
    }
    const voiceStartBtn = document.getElementById("voiceStartBtn");
    if (voiceStartBtn) {
      voiceStartBtn.addEventListener("click", async () => {
        try {
          await startVoiceSession();
        } catch (err) {
          appendVoiceLog(err.message || "Voice start failed");
          showToast(`Voice 启动失败: ${err.message || err}`, "error");
        }
      });
    }
    const voiceStopBtn = document.getElementById("voiceStopBtn");
    if (voiceStopBtn) {
      voiceStopBtn.addEventListener("click", async () => {
        try {
          await stopVoiceSession();
        } catch (err) {
          showToast(`Voice 停止失败: ${err.message || err}`, "error");
        }
      });
    }
    const voiceCopyBtn = document.getElementById("voiceCopyBtn");
    if (voiceCopyBtn) {
      voiceCopyBtn.addEventListener("click", () => {
        const token = String(document.getElementById("voiceTokenOutput")?.value || "");
        if (!token) {
          showToast("暂无可复制 Token", "info");
          return;
        }
        copyToClipboard(token);
      });
    }
    const voiceClearLogBtn = document.getElementById("voiceClearLogBtn");
    if (voiceClearLogBtn) {
      voiceClearLogBtn.addEventListener("click", () => clearVoiceLog());
    }
    const voiceName = document.getElementById("voiceName");
    const voicePersonality = document.getElementById("voicePersonality");
    const voiceSpeed = document.getElementById("voiceSpeed");
    [voiceName, voicePersonality, voiceSpeed].forEach((input) => {
      if (!input) return;
      input.addEventListener("change", updateVoiceMeta);
      input.addEventListener("input", updateVoiceMeta);
    });

    const cacheRefreshBtn = document.getElementById("cacheRefreshBtn");
    if (cacheRefreshBtn) {
      cacheRefreshBtn.addEventListener("click", async () => {
        try {
          await refreshCacheView();
          showToast("缓存已刷新", "success");
        } catch (err) {
          showToast(`刷新失败: ${err.message || err}`, "error");
        }
      });
    }
    const cacheClearBtn = document.getElementById("cacheClearBtn");
    if (cacheClearBtn) {
      cacheClearBtn.addEventListener("click", async () => {
        try {
          await clearCache();
        } catch (err) {
          showToast(`清空失败: ${err.message || err}`, "error");
        }
      });
    }
    const cacheFilter = document.getElementById("cacheTypeFilter");
    if (cacheFilter) {
      cacheFilter.addEventListener("change", async () => {
        try {
          await loadCacheList();
        } catch (err) {
          showToast(`加载失败: ${err.message || err}`, "error");
        }
      });
    }

    const cacheListBody = document.getElementById("cacheListBody");
    if (cacheListBody) {
      cacheListBody.addEventListener("click", async (event) => {
        const btn = event.target.closest(".cache-delete-btn");
        if (!btn || !cacheListBody.contains(btn)) return;
        const mediaType = decodeURIComponent(btn.dataset.mediaType || "");
        const name = decodeURIComponent(btn.dataset.name || "");
        if (!mediaType || !name) return;
        if (!window.confirm(`确认删除 ${mediaType}/${name} ?`)) return;
        try {
          await deleteCacheItem(mediaType, name);
          showToast("删除成功", "success");
        } catch (err) {
          showToast(`删除失败: ${err.message || err}`, "error");
        }
      });
    }

    const cacheOnlineBody = document.getElementById("cacheOnlineBody");
    if (cacheOnlineBody) {
      cacheOnlineBody.addEventListener("change", (event) => {
        const input = event.target.closest(".cache-online-check");
        if (!input || !cacheOnlineBody.contains(input)) return;
        const token = normalizeOnlineToken(decodeURIComponent(input.dataset.token || ""));
        if (!token) return;
        if (input.checked) {
          cacheOnlineState.selectedTokens.add(token);
        } else {
          cacheOnlineState.selectedTokens.delete(token);
        }
        syncCacheOnlineSelectAll();
      });
      cacheOnlineBody.addEventListener("click", async (event) => {
        const btn = event.target.closest(".cache-online-clear-btn");
        if (!btn || !cacheOnlineBody.contains(btn)) return;
        const token = normalizeOnlineToken(decodeURIComponent(btn.dataset.token || ""));
        if (!token) return;
        try {
          await clearSingleOnlineAssets(token);
        } catch (err) {
          showToast(`在线清理失败: ${err.message || err}`, "error");
        }
      });
    }

    const cacheOnlineSelectAll = document.getElementById("cacheOnlineSelectAll");
    if (cacheOnlineSelectAll) {
      cacheOnlineSelectAll.addEventListener("change", () => {
        const body = document.getElementById("cacheOnlineBody");
        if (!body) return;
        const checked = !!cacheOnlineSelectAll.checked;
        const checkboxes = Array.from(body.querySelectorAll("input.cache-online-check"));
        checkboxes.forEach((item) => {
          const token = normalizeOnlineToken(decodeURIComponent(item.dataset.token || ""));
          if (!token) return;
          item.checked = checked;
          if (checked) {
            cacheOnlineState.selectedTokens.add(token);
          } else {
            cacheOnlineState.selectedTokens.delete(token);
          }
        });
        syncCacheOnlineSelectAll();
      });
    }

    const cacheOnlineLoadSelectedBtn = document.getElementById("cacheOnlineLoadSelectedBtn");
    if (cacheOnlineLoadSelectedBtn) {
      cacheOnlineLoadSelectedBtn.addEventListener("click", async () => {
        try {
          await loadSelectedOnlineStats();
        } catch (err) {
          showToast(`加载失败: ${err.message || err}`, "error");
        }
      });
    }
    const cacheOnlineLoadAllBtn = document.getElementById("cacheOnlineLoadAllBtn");
    if (cacheOnlineLoadAllBtn) {
      cacheOnlineLoadAllBtn.addEventListener("click", async () => {
        try {
          await loadAllOnlineStats();
        } catch (err) {
          showToast(`加载失败: ${err.message || err}`, "error");
        }
      });
    }
    const cacheOnlineClearSelectedBtn = document.getElementById("cacheOnlineClearSelectedBtn");
    if (cacheOnlineClearSelectedBtn) {
      cacheOnlineClearSelectedBtn.addEventListener("click", async () => {
        try {
          await clearSelectedOnlineAssets();
        } catch (err) {
          showToast(`在线清理失败: ${err.message || err}`, "error");
        }
      });
    }
    const cacheOnlineBatchCancelBtn = document.getElementById("cacheOnlineBatchCancelBtn");
    if (cacheOnlineBatchCancelBtn) {
      cacheOnlineBatchCancelBtn.addEventListener("click", async () => {
        try {
          await cancelCacheBatchTask();
        } catch (err) {
          showToast(`取消失败: ${err.message || err}`, "error");
        }
      });
    }
  }

  async function init() {
    await initChat();
    bindEvents();
    window.addEventListener("beforeunload", () => {
      if (Array.isArray(imagineState.taskIDs) && imagineState.taskIDs.length > 0) {
        closeImagineConnections(true);
        try {
          const payload = JSON.stringify({ task_ids: imagineState.taskIDs });
          navigator.sendBeacon(
            "/api/v1/admin/imagine/stop",
            new Blob([payload], { type: "application/json" }),
          );
        } catch (err) {
          // ignore unload errors
        }
      }
      closeVideoStream();
      stopVideoElapsedTimer();
      if (videoState.taskID) {
        try {
          const payload = JSON.stringify({ task_ids: [videoState.taskID] });
          navigator.sendBeacon(
            "/api/v1/admin/video/stop",
            new Blob([payload], { type: "application/json" }),
          );
        } catch (err) {
          // ignore
        }
      }
      stopVoiceSession().catch(() => {});
      closeCacheBatchStream();
    });
    const uiState = loadGrokToolsUIState();
    switchGrokToolTab(String(uiState.activeToolTab || "chat"));
    resetImagineMetrics();
    updateImagineActiveCount();
    clearVideoOutput();
    setVideoButtons(false);
    setVideoStatus("未启动");
    updateVoiceMeta();
    initVoiceVisualizer();
    stopVoiceVisualizer();
    setVoiceButtons(false);
    setVoiceStatus("未连接");
    updateCacheBatchUI();
    syncImagineModeUI(document.getElementById("imagineMode")?.value || "auto");
    try {
      await refreshCacheView();
    } catch (err) {
      showToast(`缓存加载失败: ${err.message || err}`, "error");
    }
  }

  document.addEventListener("DOMContentLoaded", init);
})();

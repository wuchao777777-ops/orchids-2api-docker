const { chromium } = require("playwright-core");

function readStdin() {
  return new Promise((resolve, reject) => {
    let data = "";
    process.stdin.setEncoding("utf8");
    process.stdin.on("data", (chunk) => {
      data += chunk;
    });
    process.stdin.on("end", () => resolve(data));
    process.stdin.on("error", reject);
  });
}

function normalizeModel(model) {
  const value = String(model || "").trim().toLowerCase();
  if (!value || value === "v0" || value === "v0-max") return "v0-max";
  if (["v0-auto", "v0-mini", "v0-pro", "v0-max-fast"].includes(value)) return value;
  return value.startsWith("v0-") ? value : "v0-max";
}

function normalizeChatId(chatId) {
  const value = String(chatId || "").trim();
  if (!value || value.startsWith("chat_")) return "";
  return value;
}

async function launchBrowser() {
  const channels = String(process.env.V0_WEB_BROWSER_CHANNEL || "chrome,msedge")
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);

  let lastError = null;
  for (const channel of channels) {
    try {
      const browser = await chromium.launch({
        channel,
        headless: true,
      });
      return { browser, channel };
    } catch (error) {
      lastError = error;
    }
  }
  throw lastError || new Error("unable to launch a Chromium-based browser");
}

function cleanParagraphs(items) {
  return items
    .map((item) => String(item || "").trim())
    .filter(Boolean)
    .join("\n\n")
    .trim();
}

function extractChatIdFromUrl(urlString) {
  try {
    const url = new URL(urlString);
    const parts = url.pathname.split("/").filter(Boolean);
    if (parts.length >= 2 && parts[0] === "chat") {
      return parts[1];
    }
  } catch (_) {
  }
  return "";
}

function modelLabel(model) {
  switch (String(model || "").trim().toLowerCase()) {
    case "v0-auto":
      return "v0 Auto";
    case "v0-mini":
      return "v0 Mini";
    case "v0-pro":
      return "v0 Pro";
    case "v0-max-fast":
      return "v0 Max Fast";
    case "v0-max":
    default:
      return "v0 Max";
  }
}

async function main() {
  const raw = await readStdin();
  const payload = JSON.parse(raw || "{}");
  const userSession = String(payload.userSession || "").trim();
  const prompt = String(payload.prompt || "").trim();
  const timeoutMs = Number(payload.timeoutMs || 180000);
  const model = normalizeModel(payload.model);
  const chatId = normalizeChatId(payload.chatId);

  if (!userSession) {
    throw new Error("missing userSession");
  }
  if (!prompt) {
    throw new Error("missing prompt");
  }

  const { browser } = await launchBrowser();
  const context = await browser.newContext({
    locale: "zh-CN",
    viewport: { width: 1440, height: 900 },
  });

  try {
    await context.addCookies([{
      name: "user_session",
      value: userSession,
      domain: "v0.app",
      path: "/",
      httpOnly: true,
      secure: true,
      sameSite: "None",
    }]);

    const page = await context.newPage();
    const targetURL = chatId ? `https://v0.app/chat/${chatId}` : "https://v0.app/chat";
    await page.goto(targetURL, { waitUntil: "domcontentloaded", timeout: timeoutMs });

    const textbox = page.getByRole("textbox").first();
    await textbox.waitFor({ state: "visible", timeout: timeoutMs });

    const desiredLabel = modelLabel(model);
    const modelButton = page.getByRole("button", { name: /select model/i }).first();
    await modelButton.waitFor({ state: "visible", timeout: timeoutMs });
    const currentLabel = await modelButton.textContent().catch(() => "");
    if (!String(currentLabel || "").includes(desiredLabel)) {
      await modelButton.click();
      const menuItem = page.getByRole("menuitem", { name: desiredLabel, exact: true }).first();
      await menuItem.waitFor({ state: "visible", timeout: timeoutMs });
      await menuItem.click();
      await page.waitForTimeout(300);
    }

    const mainItems = page.locator("main").getByRole("listitem");
    const beforeCount = await mainItems.count();
    const beforeText = beforeCount > 0
      ? cleanParagraphs(await mainItems.last().locator("p").allTextContents())
      : "";

    await textbox.fill(prompt);

    const sendButton = page.getByTestId("prompt-form-send-button");
    await sendButton.waitFor({ state: "visible", timeout: timeoutMs });
    await sendButton.click();

    await page.waitForURL(/\/chat\//, { timeout: timeoutMs }).catch(() => {});

    const stopButton = page.getByRole("button", { name: /停止|Stop/i });
    await stopButton.waitFor({ state: "visible", timeout: Math.min(timeoutMs, 10000) }).catch(() => {});
    await stopButton.waitFor({ state: "hidden", timeout: timeoutMs }).catch(() => {});

    await page.waitForFunction(
      ({ count, text, promptText }) => {
        const nodes = Array.from(document.querySelectorAll("main [role='listitem'] p"));
        const values = nodes.map((node) => (node.innerText || "").trim()).filter(Boolean);
        const latest = values.length > 0 ? values[values.length - 1] : "";
        const itemCount = document.querySelectorAll("main [role='listitem']").length;
        return itemCount > count || (latest && latest !== text && latest !== promptText);
      },
      { count: beforeCount, text: beforeText, promptText: prompt },
      { timeout: timeoutMs }
    ).catch(() => {});

    const afterCount = await mainItems.count();
    if (afterCount === 0) {
      throw new Error("no chat messages were found after submitting the prompt");
    }

    const reply = cleanParagraphs(await mainItems.last().locator("p").allTextContents());
    if (!reply || reply === prompt || reply === beforeText) {
      throw new Error("assistant reply was not detected");
    }

    const resolvedChatId = extractChatIdFromUrl(page.url());
    process.stdout.write(JSON.stringify({
      ok: true,
      chatId: resolvedChatId,
      model,
      response: reply,
    }));
  } finally {
    await context.close().catch(() => {});
    await browser.close().catch(() => {});
  }
}

main().catch((error) => {
  process.stdout.write(JSON.stringify({
    ok: false,
    error: error && error.message ? String(error.message) : "unknown error",
  }));
  process.exitCode = 1;
});

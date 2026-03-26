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
      return browser;
    } catch (error) {
      lastError = error;
    }
  }
  throw lastError || new Error("unable to launch a Chromium-based browser");
}

function normalizeModelID(text) {
  const value = String(text || "").trim().toLowerCase();
  if (!value) return "";
  if (value === "v0 auto" || value === "v0-auto") return "v0-auto";
  if (value === "v0 max" || value === "v0-max") return "v0-max";
  if (value === "v0 mini" || value === "v0-mini") return "v0-mini";
  if (value === "v0 pro" || value === "v0-pro") return "v0-pro";
  if (value === "v0 max fast" || value === "v0-max-fast") return "v0-max-fast";
  return value
    .replace(/\s+/g, "-")
    .replace(/[^a-z0-9._-]/g, "")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "");
}

function dedupeModels(items) {
  const seen = new Set();
  const out = [];
  for (const item of items) {
    const name = String(item && item.name ? item.name : "").trim();
    const id = String(item && item.id ? item.id : "").trim();
    if (!id || !name || seen.has(id)) continue;
    seen.add(id);
    out.push({ id, name });
  }
  return out;
}

async function extractModels(page) {
  const items = page.getByRole("menuitem");
  const count = await items.count();
  const out = [];
  for (let i = 0; i < count; i += 1) {
    const text = String(await items.nth(i).textContent().catch(() => "") || "")
      .replace(/\s+/g, " ")
      .trim();
    if (text) out.push(text);
  }
  return out;
}

async function main() {
  const raw = await readStdin();
  const payload = JSON.parse(raw || "{}");
  const userSession = String(payload.userSession || "").trim();
  const timeoutMs = Number(payload.timeoutMs || 60000);

  if (!userSession) {
    throw new Error("missing userSession");
  }

  const browser = await launchBrowser();
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
    await page.goto("https://v0.app/chat", { waitUntil: "domcontentloaded", timeout: timeoutMs });

    const textbox = page.getByRole("textbox").first();
    await textbox.waitFor({ state: "visible", timeout: timeoutMs });

    const modelButton = page.getByRole("button", { name: /select model/i }).first();
    await modelButton.waitFor({ state: "visible", timeout: timeoutMs });
    await modelButton.click({ force: true });
    await page.getByRole("menuitem").first().waitFor({ state: "visible", timeout: timeoutMs });
    const names = await extractModels(page);

    const models = dedupeModels(
      names.map((name) => ({
        id: normalizeModelID(name),
        name,
      })),
    );

    process.stdout.write(JSON.stringify({
      ok: true,
      models,
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

const chainEl = document.getElementById("chain");
const summaryEl = document.getElementById("summary");
const staleEl = document.getElementById("stale");
const stampEl = document.getElementById("stamp");
let currentStamp = "";
let staleNotices = [];

function badge(check) {
  if (!check.passed) return '<span class="tag fail">失败</span>';
  if (check.blocked) return '<span class="tag blocked">受阻</span>';
  return '<span class="tag pass">通过</span>';
}

function versionRows(versions) {
  if (!versions || !versions.length) return "";
  return `<dl class="versions">${versions.map(v => `<dt>${v.subject}</dt><dd>${v.kind} = ${v.value}</dd>`).join("")}</dl>`;
}

function render(report) {
  if (currentStamp && report.stamp !== currentStamp) {
    staleNotices.unshift(`上一份诊断（状态指纹 ${currentStamp}）已失效：引用的状态版本在此之后发生了变化`);
    staleNotices = staleNotices.slice(0, 3);
  }
  currentStamp = report.stamp;
  staleEl.innerHTML = staleNotices.map(note => `<p class="stale-note">${note}</p>`).join("");
  summaryEl.innerHTML = `<article class="item ${report.healthy ? "" : "unhealthy"}"><h2>${report.healthy ? "链路健康" : "链路存在阻断"}</h2><p>状态指纹 ${report.stamp} · 检查于 ${new Date(report.checked_at).toLocaleTimeString()}</p>${report.first_blocker ? `<p class="blocker">最早阻断点：${report.first_blocker}</p>` : ""}${report.advice ? `<p class="advice">处理建议：${report.advice}</p>` : ""}</article>`;
  chainEl.innerHTML = report.checks.map((check, index) => `${index ? '<div class="link">↓</div>' : ""}<article class="item node ${check.passed ? "" : "fail"} ${check.blocked ? "blocked" : ""} ${check.name === report.first_blocker ? "first" : ""}"><h2>${index + 1}. ${check.name} ${badge(check)}</h2><p>${check.detail || ""}</p>${check.impact ? `<p class="impact">影响：${check.impact}</p>` : ""}${check.depends_on && check.depends_on.length ? `<p class="deps">依赖：${check.depends_on.join("、")}</p>` : ""}${versionRows(check.versions)}</article>`).join("");
  stampEl.textContent = `状态指纹 ${report.stamp} · ${new Date().toLocaleTimeString()}`;
}

async function refresh() {
  try {
    const response = await fetch("/api/diagnostics", { headers: { Accept: "application/json" } });
    render(await response.json());
  } catch (error) {
    chainEl.innerHTML = `<p class="empty">服务连接失败 ${error.message}</p>`;
    stampEl.textContent = "连接已中断";
  }
}
refresh();
setInterval(refresh, 5000);

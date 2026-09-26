const endpoint=document.body.dataset.endpoint;
const content=document.getElementById("content");
const summary=document.getElementById("summary");
const stamp=document.getElementById("stamp");
const rerun=document.getElementById("rerun");

function label(value){return String(value).replaceAll("_"," ")}
function esc(value){return String(value??"").replace(/[&<>"']/g,ch=>({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[ch]))}

const STATUS_TEXT={passed:"通过",failed:"失败",blocked:"被阻断"};

function checkCard(check,index){
  const badge=`<span class="badge ${esc(check.status)}">${STATUS_TEXT[check.status]||esc(check.status)}</span>`;
  const stale=check.stale
    ? `<span class="badge stale">已失效：${check.changed_states.map(esc).join("、")} 已变更</span>`
    : "";
  const deps=check.depends_on?.length
    ? `<dt>上游依赖</dt><dd>${check.depends_on.map(esc).join("、")}</dd>`
    : `<dt>上游依赖</dt><dd>链首环节</dd>`;
  const blocked=check.blocked_by
    ? `<dt>阻断来源</dt><dd class="fault">${esc(check.blocked_by)}</dd>`
    : "";
  const refs=`<dt>状态版本</dt><dd>${check.state_refs.map(ref=>`${esc(ref.component)}@v${ref.version}`).join("<br>")}</dd>`;
  const impact=check.impact?.length
    ? `<dt>下游影响</dt><dd>${check.impact.map(esc).join("、")}</dd>`
    : "";
  const action=check.recommendation
    ? `<p class="action">建议：${esc(check.recommendation)}</p>`
    : "";
  const arrow=index>0?`<div class="arrow" aria-hidden="true">↑ 依赖</div>`:"";
  return `${arrow}<article class="stage ${esc(check.status)}${check.stale?" stale":""}">
    <h2>${label(check.name)} ${badge}${stale}</h2>
    <p class="detail">${esc(check.detail)}</p>
    <dl>${deps}${blocked}${refs}${impact}</dl>
    ${action}
  </article>`;
}

function renderSummary(report){
  const head=report.healthy
    ? `<p class="banner ok">依赖链全部通过，最早阻断点不存在</p>`
    : `<p class="banner fault">最早阻断点：<strong>${esc(report.root_blocker)}</strong></p>
       <p class="action">建议先处理：${esc(report.recommended_first_action)}</p>`;
  const stale=report.stale
    ? `<p class="banner stale">本报告已失效：自 ${new Date(report.checked_at).toLocaleTimeString()} 起 ${report.changed_states.map(esc).join("、")} 已变更，请重新诊断。</p>`
    : `<p class="muted">报告版本有效，引用状态未变化</p>`;
  summary.innerHTML=head+stale;
}

async function load(){
  try{
    const response=await fetch(endpoint,{headers:{Accept:"application/json"}});
    const report=await response.json();
    renderSummary(report);
    content.innerHTML=report.checks.map(checkCard).join("");
    stamp.textContent=`状态 ${response.status} · 诊断于 ${new Date(report.checked_at).toLocaleTimeString()} · ${new Date().toLocaleTimeString()} 刷新`;
  }catch(error){
    summary.innerHTML=`<p class="banner fault">服务连接失败 ${esc(error.message)}</p>`;
    stamp.textContent="连接已中断";
  }
}

rerun.addEventListener("click",async()=>{
  rerun.disabled=true;
  try{
    const response=await fetch(endpoint,{method:"POST",headers:{Accept:"application/json"}});
    const report=await response.json();
    renderSummary(report);
    content.innerHTML=report.checks.map(checkCard).join("");
    stamp.textContent=`状态 ${response.status} · 诊断于 ${new Date(report.checked_at).toLocaleTimeString()} 重新评估`;
  }catch(error){
    summary.innerHTML=`<p class="banner fault">重新诊断失败 ${esc(error.message)}</p>`;
  }finally{
    rerun.disabled=false;
  }
});

load();
setInterval(load,5000);

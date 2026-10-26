package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// WebServer — статус-сторінка:
//
//	GET /          — HTML-сторінка стану (автооновлення)
//	GET /api/state — JSON зі станом (як /api/state у прошивці)
//
// Без авторизації: слухати тільки внутрішню мережу/localhost.
type WebServer struct {
	log *Logger
	mgr *Manager

	mu     sync.Mutex
	listen string
	srv    *http.Server
}

func newWebServer(log *Logger, mgr *Manager, listen string) *WebServer {
	return &WebServer{log: log, mgr: mgr, listen: listen}
}

func (w *WebServer) Start() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.startLocked()
}

func (w *WebServer) startLocked() {
	if w.listen == "" || w.listen == "-" {
		w.log.Infof("web status page disabled (web_listen empty)")
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", w.handleState)
	mux.HandleFunc("/", w.handleIndex)
	srv := &http.Server{
		Addr:              w.listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	w.srv = srv
	go func() {
		// у фоновому режимі; помилка бінду не вбиває процес
		w.log.Infof("web status page listening on http://%s", w.listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			w.log.Errorf("web server: %v (status page unavailable)", err)
		}
	}()
}

// Apply перезапускає сервер, якщо адреса прослуховування змінилась (SIGHUP).
func (w *WebServer) Apply(listen string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if listen == w.listen {
		return
	}
	w.stopLocked()
	w.listen = listen
	w.startLocked()
}

func (w *WebServer) stopLocked() {
	if w.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = w.srv.Shutdown(ctx)
	w.srv = nil
}

func (w *WebServer) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopLocked()
}

func (w *WebServer) handleState(rw http.ResponseWriter, req *http.Request) {
	g := w.mgr.Snapshot()
	data, err := json.Marshal(g)
	if err != nil {
		http.Error(rw, "internal error", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-store")
	_, _ = rw.Write(data)
}

func (w *WebServer) handleIndex(rw http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/" && req.URL.Path != "/index.html" {
		http.NotFound(rw, req)
		return
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = rw.Write([]byte(indexHTML))
}

const indexHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>svitlobot — status</title>
<style>
  :root[data-theme="dark"]{
    color-scheme:dark;
    --bg:#0b0d12;
    --dot:rgba(255,255,255,.045);
    --surface:#14161d;
    --surface-2:#101219;
    --border:rgba(255,255,255,.09);
    --hairline:rgba(255,255,255,.06);
    --tx:#f1f4fa; --dim:#98a2b8; --faint:#6d7789;
    --up:#34d399; --up-tint:rgba(52,211,153,.09);
    --warn:#fbbf24; --warn-tint:rgba(251,191,36,.09);
    --down:#f87171; --down-tint:rgba(248,113,113,.09);
    --shadow:0 18px 44px rgba(0,0,0,.4);
  }
  :root[data-theme="light"]{
    color-scheme:light;
    --bg:#f3f5f8;
    --dot:rgba(17,24,39,.055);
    --surface:#ffffff;
    --surface-2:#fbfcfe;
    --border:rgba(17,24,39,.09);
    --hairline:rgba(17,24,39,.06);
    --tx:#171c28; --dim:#5d6675; --faint:#8b94a4;
    --up:#10b981; --up-tint:rgba(16,185,129,.1);
    --warn:#d97706; --warn-tint:rgba(217,119,6,.1);
    --down:#ef4444; --down-tint:rgba(239,68,68,.1);
    --shadow:0 12px 34px rgba(23,31,46,.09);
  }
  *{box-sizing:border-box}
  html,body{margin:0;padding:0}
  body{
    background-color:var(--bg);
    background-image:radial-gradient(var(--dot) 1px, transparent 1px);
    background-size:22px 22px;
    color:var(--tx);
    font:15px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;
    min-height:100vh;
    -webkit-font-smoothing:antialiased;
  }
  .wrap{max-width:1060px;margin:0 auto;padding:36px 22px 50px}

  /* ---------- header ---------- */
  header{display:flex;align-items:center;gap:12px;flex-wrap:wrap;margin-bottom:22px}
  .logo{width:34px;height:34px;border-radius:9px;flex:none;display:flex;align-items:center;justify-content:center;
        background:var(--up-tint);border:1px solid var(--up)}
  .logo::before{content:"";width:9px;height:9px;border-radius:50%;background:var(--up);animation:pulse 2.4s infinite}
  @keyframes pulse{0%{box-shadow:0 0 0 0 currentColor;opacity:1}70%{box-shadow:0 0 0 7px transparent}100%{box-shadow:0 0 0 0 transparent}}
  h1{font-size:1.05rem;font-weight:700;margin:0;letter-spacing:.2px;white-space:nowrap}
  .spacer{flex:1}
  .Btnchip{background:var(--surface);border:1px solid var(--border);color:var(--dim);
           padding:6px 14px;border-radius:999px;font-size:.78rem;font-weight:600;cursor:pointer}
  .Btnchip:hover{color:var(--tx);border-color:var(--border)}
  .chips{display:flex;gap:7px;flex-wrap:wrap}
  .chip{background:var(--surface-2);border:1px solid var(--border);color:var(--dim);
        padding:5px 13px;border-radius:999px;font-size:.76rem;white-space:nowrap}
  .chip b{color:var(--tx);font-weight:650;font-variant-numeric:tabular-nums}

  /* ---------- summary strip (multi-host) ---------- */
  .sumrow{display:flex;gap:10px;flex-wrap:wrap;margin-bottom:16px}
  .pill{display:inline-flex;align-items:center;gap:8px;background:var(--surface);
        border:1px solid var(--border);border-radius:999px;padding:8px 16px;font-size:.82rem;color:var(--dim)}
  .pill i{width:8px;height:8px;border-radius:50%}
  .pill b{color:var(--tx);font-weight:700;font-variant-numeric:tabular-nums}
  .pill i.up{background:var(--up)} .pill i.warn{background:var(--warn)} .pill i.down{background:var(--down)}

  /* ---------- single-host hero ---------- */
  .hero{position:relative;border:1px solid var(--border);border-radius:20px;background:var(--surface);
        box-shadow:var(--shadow);padding:26px 28px 24px;overflow:hidden}
  .hero.up::after, .hero.grace::after, .hero.down::after{
    content:"";position:absolute;inset:0;pointer-events:none;opacity:.9;
  }
  .hero.up{background-image:linear-gradient(160deg,var(--up-tint),transparent 46%),var(--surface)}
  .hero.grace{background-image:linear-gradient(160deg,var(--warn-tint),transparent 46%),var(--surface)}
  .hero.down{background-image:linear-gradient(160deg,var(--down-tint),transparent 46%),var(--surface)}
  .status-line{display:flex;align-items:center;gap:11px;margin-bottom:6px}
  .sdot{width:12px;height:12px;border-radius:50%;flex:none}
  .hero.up .sdot{background:var(--up);box-shadow:0 0 12px var(--up)}
  .hero.grace .sdot{background:var(--warn);box-shadow:0 0 12px var(--warn);animation:blink 1.8s ease-in-out infinite}
  .hero.down .sdot{background:var(--down);box-shadow:0 0 12px var(--down)}
  @keyframes blink{0%,100%{opacity:1}50%{opacity:.25}}
  .status-line h2{font-size:1.55rem;font-weight:750;margin:0;letter-spacing:-.3px}
  .hero.up .status-line h2{color:var(--up)}
  .hero.grace .status-line h2{color:var(--warn)}
  .hero.down .status-line h2{color:var(--down)}
  .tline{color:var(--dim);font-size:.84rem;margin-bottom:16px;word-break:break-all}
  .tline code{font-family:ui-monospace,"SF Mono",Menlo,Consolas,monospace;font-size:.8rem;color:var(--tx);
              background:var(--surface-2);border:1px solid var(--border);padding:2px 8px;border-radius:7px}
  .tline .warn{color:var(--warn)}
  .note{font-size:.8rem;color:var(--faint);margin-top:12px}
  .tiles{display:grid;grid-template-columns:repeat(4,1fr);gap:10px}
  .tile{background:var(--surface-2);border:1px solid var(--border);border-radius:12px;padding:10px 14px;min-width:0}
  .tile .k{color:var(--faint);font-size:.64rem;text-transform:uppercase;letter-spacing:1.1px;margin-bottom:3px}
  .tile .v{font-size:.9rem;font-weight:650;font-variant-numeric:tabular-nums;word-break:break-all;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
  .tile .v .ok{color:var(--up)} .tile .v .err{color:var(--down)} .tile .v .warn{color:var(--warn)}
  @media (max-width:640px){.tiles{grid-template-columns:repeat(2,1fr)}}

  /* ---------- host cards (multi) ---------- */
  .grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(330px,1fr));gap:14px;margin-top:8px}
  .card{background:var(--surface);border:1px solid var(--border);border-radius:16px;padding:18px 20px 14px;
        box-shadow:var(--shadow);transition:transform .18s ease;border-left:none}
  .card:hover{transform:translateY(-2px)}
  .chead{display:flex;align-items:center;gap:10px;margin-bottom:6px}
  .dot{width:9px;height:9px;border-radius:50%;flex:none}
  .card.up .dot{background:var(--up);box-shadow:0 0 9px var(--up)}
  .card.grace .dot{background:var(--warn);box-shadow:0 0 9px var(--warn);animation:blink 1.8s ease-in-out infinite}
  .card.down .dot{background:var(--down);box-shadow:0 0 9px var(--down)}
  .name{font-weight:650;font-size:1rem;flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
  .badge{font-size:.7rem;font-weight:750;letter-spacing:.8px;padding:4px 11px;border-radius:999px;white-space:nowrap}
  .badge.up{background:var(--up-tint);color:var(--up)}
  .badge.grace{background:var(--warn-tint);color:var(--warn)}
  .badge.down{background:var(--down-tint);color:var(--down)}
  .card .tline{font-size:.82rem;margin-bottom:12px}
  dl{margin:0}
  .rows{border-top:1px solid var(--hairline)}
  .row{display:flex;justify-content:space-between;align-items:baseline;gap:14px;padding:7px 0;border-bottom:1px solid var(--hairline)}
  .row:last-child{border-bottom:0}
  dt{color:var(--faint);font-size:.76rem;flex:none}
  dd{margin:0;text-align:right;font-size:.8rem;color:var(--tx);min-width:0;word-break:break-all;font-variant-numeric:tabular-nums}
  dd .ok{color:var(--up)} dd .err{color:var(--down)} dd .warn{color:var(--warn)}
  footer{margin-top:32px;color:var(--faint);font-size:.73rem;text-align:center}
  footer code{font-family:ui-monospace,Menlo,Consolas,monospace;font-size:.72rem;color:var(--dim)}
  .empty{background:var(--surface);border:1px dashed var(--border);border-radius:16px;
         padding:40px;text-align:center;color:var(--dim)}
  .errbox{border-color:var(--down);color:var(--down)}
  @media (prefers-reduced-motion:reduce){*{animation:none!important;transition:none!important}}
</style>
</head>
<body>
<div class="wrap">
  <header>
    <span class="logo"></span>
    <h1>svitlobot</h1>
    <span class="spacer"></span>
    <button class="Btnchip" id="themeBtn" type="button"></button>
    <div class="chips" id="chips"></div>
  </header>

  <div class="sumrow" id="summary" style="display:none"></div>
  <div class="grid" id="hosts"></div>
  <footer>heartbeats to SvitloBot &middot; raw state: <code>/api/state</code></footer>
</div>

<script>
function esc(s){
  if(s===null||s===undefined||s==="") return "&mdash;";
  return String(s).replace(/[<>&"]/g,function(c){
    return {"<":"&lt;",">":"&gt;","&":"&amp;","\"":"&quot;"}[c];
  });
}
function ago(iso){
  if(!iso) return "never";
  var s=Math.max(0,Math.round((Date.now()-new Date(iso).getTime())/1000));
  if(s<60) return s+"s ago";
  var m=Math.floor(s/60); if(m<60) return m+"m ago";
  var h=Math.floor(m/60); if(h<24) return h+"h "+(m%60)+"m ago";
  return Math.floor(h/24)+"d "+(h%24)+"h ago";
}
function since(iso){
  if(!iso) return "&mdash;";
  var d=new Date(iso);
  function p2(n){return (n<10?"0":"")+n;}
  return d.getFullYear()+"-"+p2(d.getMonth()+1)+"-"+p2(d.getDate())+
         " "+p2(d.getHours())+":"+p2(d.getMinutes())+":"+p2(d.getSeconds());
}
function dur(upt){
  var d=Math.floor(upt/86400), h=Math.floor(upt%86400/3600);
  var m=Math.floor(upt%3600/60), s=Math.floor(upt%60), t="";
  if(d>0) t+=d+"d ";
  if(d>0||h>0) t+=h+"h ";
  if(d>0||h>0||m>0) t+=m+"m ";
  return t+s+"s";
}
function hostCls(h){
  if(h.up && h.grace) return "grace";
  if(h.up) return "up";
  return "down";
}
function badgeLabel(h,cls){
  if(cls==="up") return "UP";
  if(cls==="grace") return "DEGRADED "+h.fail_bursts+"/"+h.down_threshold;
  return "DOWN";
}
function heroTitle(cls){
  if(cls==="up") return "Power is up";
  if(cls==="grace") return "Power is flickering";
  return "Power is out";
}
function shortStatus(h){
  if(h.last_notify_code>0){
    var c=h.last_notify_code;
    var s;
    if(c<300) s="OK";
    else if(c===429) s="Rate limited";
    else if(c===401||c===403) s="Denied";
    else if(c<500) s="Rejected";
    else s="Server error";
    return {t:s, tip:"HTTP "+c, bad:c>=300};
  }
  var e=h.last_notify_err||"";
  if(!e) return null;
  var l="Failed", bad=true;
  if(/unexpected HTTP status/i.test(e)){ l=/429/.test(e)?"Rate limited":"Failed"; }
  else if(/refused/i.test(e)) l="Refused";
  else if(/deadline|timeout|timed out/i.test(e)) l="Timed out";
  else if(/no such host|lookup|dns/i.test(e)) l="DNS";
  else if(/unreachable/i.test(e)) l="Unreachable";
  return {t:l, tip:e, bad:true};
}
function statusHTML(h){
  var st=shortStatus(h);
  if(!st) return "never";
  return '<span class="'+(st.bad?"err":"ok")+'" title="'+esc(st.tip)+'">'+st.t+'</span>';
}

// ---- theme ----
var themeBtn=document.getElementById("themeBtn");
function applyTheme(t){
  document.documentElement.setAttribute("data-theme",t);
  themeBtn.textContent=(t==="dark"?"Light":"Dark");
  try{localStorage.setItem("sb-theme",t);}catch(e){}
}
var savedTheme=null;
try{savedTheme=localStorage.getItem("sb-theme");}catch(e){}
if(savedTheme!=="dark"&&savedTheme!=="light"){
  savedTheme=(window.matchMedia&&window.matchMedia("(prefers-color-scheme: light)").matches)
    ?"light":"dark";
}
applyTheme(savedTheme);
themeBtn.onclick=function(){
  applyTheme(document.documentElement.getAttribute("data-theme")==="dark"?"light":"dark");
};

function renderState(g){
  var hosts=g.hosts||[];
  var up=0, grace=0, down=0;
  hosts.forEach(function(h){
    if(h.up&&h.grace) grace++; else if(h.up) up++; else down++;
  });

  document.getElementById("chips").innerHTML=
    '<span class="chip">v<b>'+esc(g.version)+'</b></span>'+
    '<span class="chip">uptime <b>'+dur(g.uptime_seconds)+'</b></span>'+
    '<span class="chip">since <b>'+since(g.started_at)+'</b></span>';

  var single=hosts.length===1;
  var box=document.getElementById("hosts");
  var sum=document.getElementById("summary");
  sum.style.display="flex";
  document.body.classList.toggle("single",single);
  if(single){
    sum.style.display="none";
    renderHero(hosts[0]);
  }else{
    var html=
      '<span class="pill"><b>'+hosts.length+'</b> hosts</span>'+
      '<span class="pill"><i class="up"></i><b>'+up+'</b> up</span>'+
      '<span class="pill"><i class="warn"></i><b>'+grace+'</b> degraded</span>'+
      '<span class="pill"><i class="down"></i><b>'+down+'</b> down</span>';
    sum.innerHTML=single?"":html;
    box.innerHTML="";
    hosts.forEach(function(h){box.appendChild(hostCard(h));});
  }

  if(!hosts.length){
    box.innerHTML='<div class="empty">No hosts configured &mdash; add targets to config.json and send SIGHUP.</div>';
  }
}

function renderHero(h){
  var box=document.getElementById("hosts");
  var cls=hostCls(h);
  var pingOk=h.ok_in_last_burst>=h.min_successes;
  var pings='<span class="'+(pingOk?"ok":"err")+'" title="need &ge; '+h.min_successes+' ok of '+h.attempts+'">'+
            h.ok_in_last_burst+'/'+h.attempts+' ok</span>';

  var tilePings='<div class="tile"><div class="k">Pings</div><div class="v">'+pings+'</div></div>';
  var tileLastPing='<div class="tile"><div class="k">Last ping</div><div class="v">'+ago(h.last_ping_at)+'</div></div>';
  var tileLastOk='<div class="tile"><div class="k">Last success</div><div class="v">'+ago(h.last_ok_at)+'</div></div>';
  var tileNotify='<div class="tile"><div class="k">Heartbeat</div><div class="v">'+statusHTML(h)+'</div></div>';

  var extra="";
  if(h.notify_pending){
    extra='<div class="note"><span class="err">Delivery pending</span> &mdash; will retry next cycle.</div>';
  }else if(cls==="grace"){
    extra='<div class="note"><span class="warn">Network flapping</span> &mdash; grace '+
          h.fail_bursts+'/'+h.down_threshold+' before marking offline.</div>';
  }else if(cls==="down"&&h.down_threshold>1){
    extra='<div class="note">Offline after '+h.fail_bursts+'/'+h.down_threshold+' failed checks.</div>';
  }

  box.innerHTML=
    '<section class="hero '+cls+'">'+
      '<div class="status-line"><span class="sdot"></span><h2>'+heroTitle(cls)+'</h2></div>'+
      '<div class="tline"><code>'+esc(h.target)+'</code> &middot; '+esc(h.type)+
      ' &middot; every '+h.interval_seconds+'s'+(h.has_url?'':' &middot; <span class="warn">no URL</span>')+'</div>'+
      '<div class="tiles">'+tilePings+tileLastPing+tileLastOk+tileNotify+'</div>'+
      extra+
    '</section>';
}

function hostCard(h){
  var cls=hostCls(h);
  var html=
    '<div class="chead"><span class="dot"></span>'+
    '<span class="name">'+esc(h.name)+'</span>'+
    '<span class="badge '+cls+'">'+badgeLabel(h,cls)+'</span></div>'+
    '<div class="tline"><code>'+esc(h.target)+'</code> &middot; '+esc(h.type)+
    ' &middot; '+esc(h.mode)+' &middot; every '+h.interval_seconds+'s'+(h.has_url?'':' &middot; <span class="warn">no URL</span>')+'</div>'+
    '<dl class="rows">'+
    '<div class="row"><dt>Pings</dt><dd>'+
      (function(){
        var ok=h.ok_in_last_burst>=h.min_successes;
        return '<span class="'+(ok?"ok":"err")+'" title="need &ge; '+h.min_successes+' ok of '+h.attempts+'">'+
               h.ok_in_last_burst+'/'+h.attempts+' ok (need &ge; '+h.min_successes+')</span>';
      })()+'</dd></div>'+
    '<div class="row"><dt>Last ping</dt><dd>'+ago(h.last_ping_at)+'</dd></div>'+
    '<div class="row"><dt>Last success</dt><dd>'+ago(h.last_ok_at)+'</dd></div>'+
    '<div class="row"><dt>Server ping</dt><dd>'+statusHTML(h)+'</dd></div>';
  if(h.notify_pending){
    html+='<div class="row"><dt>Delivery</dt><dd><span class="warn">retrying next cycle</span></dd></div>';
  }
  if(h.down_threshold>1 && h.fail_bursts>0){
    html+='<div class="row"><dt>Fail streak</dt><dd>'+h.fail_bursts+'/'+h.down_threshold+' bursts</dd></div>';
  }
  html+='</dl>';
  var card=document.createElement("div");
  card.className="card "+cls;
  card.innerHTML=html;
  return card;
}

async function refresh(){
  try{
    var r=await fetch("/api/state",{cache:"no-store"});
    if(!r.ok) throw new Error("HTTP "+r.status);
    var g=await r.json();
    renderState(g);
  }catch(e){
    document.getElementById("summary").style.display="none";
    document.getElementById("hosts").innerHTML='<div class="grid"><div class="empty errbox">Cannot fetch state: '+
      esc(e.message)+' &mdash; retrying in 5 s.</div></div>';
  }
}
refresh();
setInterval(refresh,5000);
</script>
</body>
</html>`

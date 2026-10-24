package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// WebServer — статус-сторінка в стилі ESP32-прошивки:
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
  :root{
    --bg:#0b1220; --glow:#172a4d;
    --card:#141e33; --card-2:#18233c;
    --line:#233152; --line-soft:#1d2945;
    --tx:#e6edfa; --tx-dim:#8ea3c8; --tx-faint:#5c7196;
    --up:#34d399; --up-dim:rgba(52,211,153,.14);
    --warn:#fbbf24; --warn-dim:rgba(251,191,36,.14);
    --down:#f87171; --down-dim:rgba(248,113,113,.14);
    --ok:#4ade80; --err:#fca5a5;
    --radius:14px;
  }
  *{box-sizing:border-box}
  html,body{margin:0;padding:0}
  body{
    background:
      radial-gradient(1100px 500px at 15% -10%, var(--glow) 0%, transparent 60%),
      radial-gradient(900px 500px at 110% 110%, #101b33 0%, transparent 55%),
      var(--bg);
    color:var(--tx);
    font:15px/1.55 "Segoe UI", system-ui, -apple-system, Roboto, Helvetica, Arial, sans-serif;
    min-height:100vh;
  }
  .wrap{max-width:1040px;margin:0 auto;padding:30px 22px 46px}

  /* ---------- header ---------- */
  header{display:flex;align-items:center;gap:12px;flex-wrap:wrap;margin-bottom:14px}
  .pulse{width:14px;height:14px;border-radius:50%;background:var(--up);
         box-shadow:0 0 0 0 rgba(52,211,153,.55);animation:pulse 2.2s infinite;flex:none}
  @keyframes pulse{
    0%{box-shadow:0 0 0 0 rgba(52,211,153,.55)}
    70%{box-shadow:0 0 0 12px rgba(52,211,153,0)}
    100%{box-shadow:0 0 0 0 rgba(52,211,153,0)}
  }
  h1{font-size:1.12rem;font-weight:700;margin:0;letter-spacing:.3px;white-space:nowrap}
  .chips{display:flex;gap:8px;flex-wrap:wrap;margin-left:auto}
  .chip{background:var(--card);border:1px solid var(--line);color:var(--tx-dim);
        padding:5px 12px;border-radius:999px;font-size:.78rem;white-space:nowrap}
  .chip b{color:var(--tx);font-weight:600}

  /* ---------- summary ---------- */
  .summary{display:flex;gap:10px;flex-wrap:wrap;margin:20px 0 8px}
  .sum{flex:1 1 130px;background:var(--card);border:1px solid var(--line-soft);
       border-radius:var(--radius);padding:12px 16px}
  .sum .v{font-size:1.35rem;font-weight:700;line-height:1.2}
  .sum .k{color:var(--tx-faint);font-size:.74rem;text-transform:uppercase;letter-spacing:.8px;margin-top:2px}
  .sum.up .v{color:var(--up)} .sum.warn .v{color:var(--warn)} .sum.down .v{color:var(--down)}

  .updated{color:var(--tx-faint);font-size:.78rem;margin:2px 2px 16px}

  /* ---------- cards ---------- */
  .grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(320px,1fr));gap:16px}
  body.single .grid{grid-template-columns:1fr}
  body.single .card{padding:20px 24px 18px}
  body.single .wrap{max-width:820px}
  .card{
    background:linear-gradient(180deg,var(--card) 0%, var(--card-2) 100%);
    border:1px solid var(--line-soft);border-left:3px solid var(--tx-faint);
    border-radius:var(--radius);padding:16px 18px 14px;
    transition:transform .15s ease, box-shadow .15s ease;
  }
  .card:hover{transform:translateY(-2px);box-shadow:0 8px 24px rgba(0,0,0,.35)}
  .card.up{border-left-color:var(--up)}
  .card.grace{border-left-color:var(--warn)}
  .card.down{border-left-color:var(--down)}

  .chead{display:flex;align-items:center;gap:10px;margin-bottom:4px}
  .dot{width:9px;height:9px;border-radius:50%;flex:none}
  .card.up .dot{background:var(--up);box-shadow:0 0 8px var(--up)}
  .card.grace .dot{background:var(--warn);box-shadow:0 0 8px var(--warn);animation:blink 1.6s infinite}
  .card.down .dot{background:var(--down);box-shadow:0 0 8px var(--down)}
  @keyframes blink{0%,100%{opacity:1}50%{opacity:.35}}
  .name{font-weight:650;font-size:1.02rem;flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
  .badge{font-size:.72rem;font-weight:700;letter-spacing:.6px;padding:3px 10px;border-radius:999px;white-space:nowrap}
  .badge.up{background:var(--up-dim);color:var(--up)}
  .badge.grace{background:var(--warn-dim);color:var(--warn)}
  .badge.down{background:var(--down-dim);color:var(--down)}

  .tline{color:var(--tx-dim);font-size:.83rem;margin-bottom:12px;word-break:break-all}
  .tline code{color:var(--tx);font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;
              font-size:.82rem;background:rgba(255,255,255,.05);padding:1px 6px;border-radius:6px}

  .meter{height:6px;border-radius:4px;background:rgba(255,255,255,.06);overflow:hidden;margin-bottom:12px}
  .meter i{display:block;height:100%;border-radius:4px;background:var(--up);transition:width .4s ease}
  .card.grace .meter i{background:var(--warn)}
  .card.down .meter i{background:var(--down)}
  .mlabel{color:var(--tx-faint);font-size:.72rem;margin:-8px 0 10px}

  dl{margin:0;display:grid;grid-template-columns:auto 1fr;gap:4px 14px}
  dt{color:var(--tx-faint);font-size:.8rem;padding:2px 0}
  dd{margin:0;text-align:right;font-size:.83rem;color:var(--tx);min-width:0;word-break:break-all}
  dd .ok{color:var(--ok)} dd .err{color:var(--err);display:block;font-size:.78rem}
  dd .warn{color:var(--warn)}
  footer{margin-top:26px;color:var(--tx-faint);font-size:.75rem;text-align:center}
  .empty{background:var(--card);border:1px dashed var(--line);border-radius:var(--radius);
         padding:34px;text-align:center;color:var(--tx-dim)}
  .errbox{border-color:var(--down);color:var(--err)}
</style>
</head>
<body>
<div class="wrap">
  <header>
    <span class="pulse"></span>
    <h1>svitlobot</h1>
    <div class="chips" id="chips"></div>
  </header>

  <div class="summary" id="summary"></div>
  <div class="updated" id="updated"></div>
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
  var date=d.getFullYear()+"-"+p2(d.getMonth()+1)+"-"+p2(d.getDate());
  var time=p2(d.getHours())+":"+p2(d.getMinutes())+":"+p2(d.getSeconds());
  return date+" "+time;
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
function httpClientCell(h){
  if(h.last_notify_code>0) return '<span class="ok">HTTP '+h.last_notify_code+'</span>';
  if(h.last_notify_err) return '<span class="err">'+esc(h.last_notify_err)+'</span>';
  return "&mdash;";
}

async function refresh(){
  try{
    var r=await fetch("/api/state",{cache:"no-store"});
    if(!r.ok) throw new Error("HTTP "+r.status);
    var g=await r.json();
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
    document.body.classList.toggle("single",single);
    var sum=document.getElementById("summary");
    if(single){
      // один хост: зведений рядок не потрібен, карточка на всю ширину
      sum.style.display="none";
    }else{
      sum.style.display="flex";
      sum.innerHTML=
        '<div class="sum"><div class="v">'+hosts.length+'</div><div class="k">hosts</div></div>'+
        '<div class="sum up"><div class="v">'+up+'</div><div class="k">up</div></div>'+
        '<div class="sum warn"><div class="v">'+grace+'</div><div class="k">degraded</div></div>'+
        '<div class="sum down"><div class="v">'+down+'</div><div class="k">down</div></div>';
    }

    var now=new Date().toLocaleTimeString();
    document.getElementById("updated").textContent="last updated "+now+" (refreshes every 5 s)";

    var box=document.getElementById("hosts");
    box.innerHTML="";
    if(!hosts.length){
      box.innerHTML='<div class="empty">No hosts configured &mdash; add targets to config.json and send SIGHUP.</div>';
      return;
    }
    hosts.forEach(function(h){
      var cls=hostCls(h);
      var pct=h.attempts?Math.round(100*h.ok_in_last_burst/h.attempts):0;
      var html=
        '<div class="chead"><span class="dot"></span>'+
        '<span class="name">'+esc(h.name)+'</span>'+
        '<span class="badge '+cls+'">'+badgeLabel(h,cls)+'</span></div>'+
        '<div class="tline"><code>'+esc(h.target)+'</code> &middot; '+esc(h.type)+
        ' &middot; '+esc(h.mode)+' &middot; every '+h.interval_seconds+'s'+(h.has_url?'':' &middot; <span class="warn">no URL</span>')+'</div>'+
        '<div class="meter"><i style="width:'+pct+'%"></i></div>'+
        '<div class="mlabel">last burst: '+h.ok_in_last_burst+'/'+h.attempts+' pings ok (need &ge; '+h.min_successes+')</div>'+
        '<dl>'+
        '<dt>Last ping</dt><dd>'+ago(h.last_ping_at)+'</dd>'+
        '<dt>Last success</dt><dd>'+ago(h.last_ok_at)+'</dd>'+
        '<dt>Server ping</dt><dd>'+ago(h.last_notify_at)+' &middot; '+httpClientCell(h)+'</dd>';
      if(h.notify_pending){
        html+='<dt>Delivery</dt><dd><span class="warn">retrying next cycle</span></dd>';
      }
      if(h.down_threshold>1 && h.fail_bursts>0){
        html+='<dt>Fail streak</dt><dd>'+h.fail_bursts+'/'+h.down_threshold+' bursts</dd>';
      }
      html+='</dl>';
      var card=document.createElement("div");
      card.className="card "+cls;
      card.innerHTML=html;
      box.appendChild(card);
    });
  }catch(e){
    var fb=document.getElementById("hosts");
    fb.innerHTML='<div class="empty errbox">Cannot fetch state: '+esc(e.message)+
                  ' &mdash; retrying in 5 s.</div>';
  }
}
refresh();
setInterval(refresh,5000);
</script>
</body>
</html>
`

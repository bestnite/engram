package web

// This file holds the compile-time constant of the first-paint theme bootstrap. It is the only inline <script> on the site:
// a dark-mode user waiting for the external script to run would first see a white frame (external scripts and stylesheets are separate requests and inevitably lose to the first frame).
//
// themeBootstrapJS is the script body (without the <script> tag); it is a separate constant so that its SHA-256 can go into the CSP
// allowlist (cspScriptHash in security_headers.go). The content does not vary with the request, so a hash is used instead of a
// per-request nonce: there is no need to thread a nonce through every render call.
const themeBootstrapJS = `(function(){var s;try{s=localStorage.getItem("engram-theme")}catch(e){}var d=s==="dark"||(s!=="light"&&window.matchMedia&&window.matchMedia("(prefers-color-scheme: dark)").matches);var r=document.documentElement;r.classList.toggle("dark",d);r.style.colorScheme=d?"dark":"light";r.style.backgroundColor=d?"#09090b":"#f8fafc";var m=document.querySelector('meta[name="theme-color"]');if(m){m.setAttribute("content",d?"#09090b":"#ffffff")}})();`

// themeBootstrap is the complete fragment including the <script> tag, injected by the server into the SPA entry's <head> at wiring time
// (Shell.SetShell), before any stylesheet runs. It covers only the minimal set that must hold before the first frame — the dark class,
// color-scheme, the canvas background and theme-color — setting the background with an inline style, so that no stylesheet can win this race.
// The color values stay in sync with applyTheme in /pwa.js.
const themeBootstrap = "<script>" + themeBootstrapJS + "</script>"

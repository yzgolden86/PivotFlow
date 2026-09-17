import{c as u,j as e,r as l,a3 as x}from"./index-CowBIELS.js";/**
 * @license lucide-react v0.468.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const f=u("ChevronLeft",[["path",{d:"m15 18-6-6 6-6",key:"1wnfg3"}]]);/**
 * @license lucide-react v0.468.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const p=u("ChevronRight",[["path",{d:"m9 18 6-6-6-6",key:"mthhwq"}]]);/**
 * @license lucide-react v0.468.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const j=u("EyeOff",[["path",{d:"M10.733 5.076a10.744 10.744 0 0 1 11.205 6.575 1 1 0 0 1 0 .696 10.747 10.747 0 0 1-1.444 2.49",key:"ct8e1f"}],["path",{d:"M14.084 14.158a3 3 0 0 1-4.242-4.242",key:"151rxh"}],["path",{d:"M17.479 17.499a10.75 10.75 0 0 1-15.417-5.151 1 1 0 0 1 0-.696 10.75 10.75 0 0 1 4.446-5.143",key:"13bj9a"}],["path",{d:"m2 2 20 20",key:"1ooewy"}]]);/**
 * @license lucide-react v0.468.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const m=u("Eye",[["path",{d:"M2.062 12.348a1 1 0 0 1 0-.696 10.75 10.75 0 0 1 19.876 0 1 1 0 0 1 0 .696 10.75 10.75 0 0 1-19.876 0",key:"1nclc0"}],["circle",{cx:"12",cy:"12",r:"3",key:"1v7zrd"}]]);/**
 * @license lucide-react v0.468.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const h=u("RefreshCw",[["path",{d:"M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8",key:"v9h5vc"}],["path",{d:"M21 3v5h-5",key:"1q7to0"}],["path",{d:"M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16",key:"3uifl3"}],["path",{d:"M8 16H3v5",key:"1cv678"}]]);function v({icon:t,title:s,actions:a,className:r="",tone:c="green"}){return e.jsxs("header",{className:`page-header page-header--elevated page-header--tone-${c}${r?` ${r}`:""}`,children:[e.jsxs("div",{className:"page-header-copy",children:[e.jsx("span",{className:"page-header-icon","aria-hidden":"true",children:e.jsx(t,{size:20})}),e.jsx("div",{className:"page-header-title",children:e.jsx("h1",{children:s})})]}),a&&e.jsx("div",{className:"header-controls",children:a})]})}function y({children:t,persistent:s=!1,onDismiss:a,tone:r="success"}){const[c,i]=l.useState(!0),o=l.useRef(a);l.useEffect(()=>{o.current=a},[a]);const n=l.useCallback(()=>{i(!1),o.current?.()},[]);return l.useEffect(()=>{if(i(!0),s)return;const d=window.setTimeout(n,4500);return()=>window.clearTimeout(d)},[n,s]),c?e.jsxs("div",{className:`operation-notice operation-notice--${r}`,role:r==="error"?"alert":"status","aria-live":r==="error"?"assertive":"polite",children:[e.jsx("span",{className:"operation-notice__content",children:t}),e.jsx("button",{className:"operation-notice__dismiss",type:"button",onClick:n,"aria-label":"关闭提示",children:e.jsx(x,{size:15})})]}):null}function g({label:t="正在加载数据"}){return e.jsxs("div",{className:"content-state",role:"status",children:[e.jsx(h,{className:"spin",size:18}),t]})}function N({message:t,retry:s}){return e.jsxs("div",{className:"content-state content-state--error",role:"alert",children:[e.jsx("strong",{children:"数据加载失败"}),e.jsx("span",{children:t}),e.jsxs("button",{className:"secondary-button",type:"button",onClick:s,children:[e.jsx(h,{size:15}),"重试"]})]})}function k({label:t}){return e.jsx("div",{className:"content-state content-state--empty",children:t})}function C({value:t,change:s,placeholder:a,required:r=!1,autoComplete:c="off"}){const[i,o]=l.useState(!1),n=t.length>0;return e.jsxs("span",{className:"secret-input",children:[e.jsx("input",{required:r,type:i&&n?"text":"password",autoComplete:c,value:t,onChange:d=>s(d.target.value),placeholder:a}),n&&e.jsx("button",{type:"button",onClick:()=>o(d=>!d),"aria-label":i?"隐藏内容":"显示内容",title:i?"隐藏内容":"核对已输入的内容",children:i?e.jsx(j,{size:16}):e.jsx(m,{size:16})})]})}function w({page:t,pageSize:s,total:a,onPage:r,pageSizes:c,onPageSize:i}){const o=Math.max(1,Math.ceil(a/s));return e.jsxs("div",{className:"pagination",children:[e.jsxs("div",{className:"pagination-meta",children:[e.jsxs("span",{children:["共 ",a," 条"]}),c&&i&&e.jsxs("label",{children:["每页",e.jsx("select",{value:s,onChange:n=>i(Number(n.target.value)),children:c.map(n=>e.jsx("option",{value:n,children:n},n))}),"条"]})]}),e.jsxs("div",{children:[e.jsx("button",{className:"icon-button icon-button--surface",type:"button",disabled:t<=1,onClick:()=>r(t-1),"aria-label":"上一页",children:e.jsx(f,{size:16})}),e.jsxs("strong",{children:[t," / ",o]}),e.jsx("button",{className:"icon-button icon-button--surface",type:"button",disabled:t>=o,onClick:()=>r(t+1),"aria-label":"下一页",children:e.jsx(p,{size:16})})]})]})}function E(t){const s=t>1e10?t:t*1e3;return new Intl.DateTimeFormat("zh-CN",{month:"2-digit",day:"2-digit",hour:"2-digit",minute:"2-digit",second:"2-digit",hour12:!1}).format(new Date(s))}function M(t){return t>=.95?"success":t>=.8?"warning":"danger"}export{p as C,N as E,g as L,y as O,v as P,h as R,C as S,k as a,w as b,E as f,M as s};

import{c as d,r as l,j as t,X as m}from"./index-Bh508c7O.js";/**
 * @license lucide-react v0.468.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const f=d("ChevronLeft",[["path",{d:"m15 18-6-6 6-6",key:"1wnfg3"}]]);/**
 * @license lucide-react v0.468.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const x=d("ChevronRight",[["path",{d:"m9 18 6-6-6-6",key:"mthhwq"}]]);/**
 * @license lucide-react v0.468.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const p=d("EyeOff",[["path",{d:"M10.733 5.076a10.744 10.744 0 0 1 11.205 6.575 1 1 0 0 1 0 .696 10.747 10.747 0 0 1-1.444 2.49",key:"ct8e1f"}],["path",{d:"M14.084 14.158a3 3 0 0 1-4.242-4.242",key:"151rxh"}],["path",{d:"M17.479 17.499a10.75 10.75 0 0 1-15.417-5.151 1 1 0 0 1 0-.696 10.75 10.75 0 0 1 4.446-5.143",key:"13bj9a"}],["path",{d:"m2 2 20 20",key:"1ooewy"}]]);/**
 * @license lucide-react v0.468.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const b=d("Eye",[["path",{d:"M2.062 12.348a1 1 0 0 1 0-.696 10.75 10.75 0 0 1 19.876 0 1 1 0 0 1 0 .696 10.75 10.75 0 0 1-19.876 0",key:"1nclc0"}],["circle",{cx:"12",cy:"12",r:"3",key:"1v7zrd"}]]);/**
 * @license lucide-react v0.468.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const h=d("RefreshCw",[["path",{d:"M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8",key:"v9h5vc"}],["path",{d:"M21 3v5h-5",key:"1q7to0"}],["path",{d:"M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16",key:"3uifl3"}],["path",{d:"M8 16H3v5",key:"1cv678"}]]);function y({children:e,persistent:n=!1,onDismiss:a,tone:r="success"}){const[c,i]=l.useState(!0),o=l.useRef(a);l.useEffect(()=>{o.current=a},[a]);const s=l.useCallback(()=>{i(!1),o.current?.()},[]);return l.useEffect(()=>{if(i(!0),n)return;const u=window.setTimeout(s,4500);return()=>window.clearTimeout(u)},[s,n]),c?t.jsxs("div",{className:`operation-notice operation-notice--${r}`,role:r==="error"?"alert":"status","aria-live":r==="error"?"assertive":"polite",children:[t.jsx("span",{className:"operation-notice__content",children:e}),t.jsx("button",{className:"operation-notice__dismiss",type:"button",onClick:s,"aria-label":"关闭提示",children:t.jsx(m,{size:15})})]}):null}function v({label:e="正在加载数据"}){return t.jsxs("div",{className:"content-state",role:"status",children:[t.jsx(h,{className:"spin",size:18}),e]})}function N({message:e,retry:n}){return t.jsxs("div",{className:"content-state content-state--error",role:"alert",children:[t.jsx("strong",{children:"数据加载失败"}),t.jsx("span",{children:e}),t.jsxs("button",{className:"secondary-button",type:"button",onClick:n,children:[t.jsx(h,{size:15}),"重试"]})]})}function g({label:e}){return t.jsx("div",{className:"content-state content-state--empty",children:e})}function k({value:e,change:n,placeholder:a,required:r=!1,autoComplete:c="off"}){const[i,o]=l.useState(!1),s=e.length>0;return t.jsxs("span",{className:"secret-input",children:[t.jsx("input",{required:r,type:i&&s?"text":"password",autoComplete:c,value:e,onChange:u=>n(u.target.value),placeholder:a}),s&&t.jsx("button",{type:"button",onClick:()=>o(u=>!u),"aria-label":i?"隐藏内容":"显示内容",title:i?"隐藏内容":"核对已输入的内容",children:i?t.jsx(p,{size:16}):t.jsx(b,{size:16})})]})}function C({page:e,pageSize:n,total:a,onPage:r,pageSizes:c,onPageSize:i}){const o=Math.max(1,Math.ceil(a/n));return t.jsxs("div",{className:"pagination",children:[t.jsxs("div",{className:"pagination-meta",children:[t.jsxs("span",{children:["共 ",a," 条"]}),c&&i&&t.jsxs("label",{children:["每页",t.jsx("select",{value:n,onChange:s=>i(Number(s.target.value)),children:c.map(s=>t.jsx("option",{value:s,children:s},s))}),"条"]})]}),t.jsxs("div",{children:[t.jsx("button",{className:"icon-button icon-button--surface",type:"button",disabled:e<=1,onClick:()=>r(e-1),"aria-label":"上一页",children:t.jsx(f,{size:16})}),t.jsxs("strong",{children:[e," / ",o]}),t.jsx("button",{className:"icon-button icon-button--surface",type:"button",disabled:e>=o,onClick:()=>r(e+1),"aria-label":"下一页",children:t.jsx(x,{size:16})})]})]})}function w(e,n=0){return new Intl.NumberFormat("zh-CN",{maximumFractionDigits:n}).format(e||0)}function E(e){const n=e||0;if(n>0&&n<1e-4)return"< $0.0001";const a=n>=1?2:4;return`$${new Intl.NumberFormat("zh-CN",{maximumFractionDigits:a,minimumFractionDigits:a}).format(n)}`}function M(e){const n=e>1e10?e:e*1e3;return new Intl.DateTimeFormat("zh-CN",{month:"2-digit",day:"2-digit",hour:"2-digit",minute:"2-digit",second:"2-digit",hour12:!1}).format(new Date(n))}function R(e){return e>=.95?"success":e>=.8?"warning":"danger"}export{x as C,N as E,v as L,y as O,C as P,h as R,k as S,g as a,w as b,E as c,M as f,R as s};

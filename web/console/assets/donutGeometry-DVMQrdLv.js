import{c as i}from"./index-Qns5qlNd.js";/**
 * @license lucide-react v0.468.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const u=i("CircleDollarSign",[["circle",{cx:"12",cy:"12",r:"10",key:"1mglay"}],["path",{d:"M16 8h-6a2 2 0 1 0 0 4h4a2 2 0 1 1 0 4H8",key:"1h4pet"}],["path",{d:"M12 18V6",key:"zqpxq5"}]]);/**
 * @license lucide-react v0.468.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const y=i("Zap",[["path",{d:"M4 14a1 1 0 0 1-.78-1.63l9.9-10.2a.5.5 0 0 1 .86.46l-1.92 6.02A1 1 0 0 0 13 10h7a1 1 0 0 1 .78 1.63l-9.9 10.2a.5.5 0 0 1-.86-.46l1.92-6.02A1 1 0 0 0 11 14z",key:"1xq2db"}]]),m=Math.PI*2;function s(c,l,t){const o=c*m;return[t+l*Math.cos(o),t+l*Math.sin(o)]}function n(c){return c.toFixed(3)}function f(c,l,t,o=50){const e=Math.min(1,Math.max(0,l)),[a,$]=s(c,t,o);if(e<=0)return`M ${n(a)} ${n($)}`;if(e>=1-1e-9){const[M,p]=s(c+.5,t,o);return`M ${n(a)} ${n($)} A ${t} ${t} 0 1 1 ${n(M)} ${n(p)} A ${t} ${t} 0 1 1 ${n(a)} ${n($)}`}const[h,r]=s(c+e,t,o);return`M ${n(a)} ${n($)} A ${t} ${t} 0 ${e>.5?1:0} 1 ${n(h)} ${n(r)}`}function A(c,l){let t=0;return c.map(o=>{const e=l(o),a=Number.isFinite(e)?Math.max(0,e):0,$={item:o,start:t,share:a};return t+=a,$})}export{u as C,y as Z,f as a,A as d};

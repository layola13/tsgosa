interface P { x: i32 }
interface Q { name: string; n: i32 }
const O: P = {x: 1};
const W: Q = {name: "hi", n: 0};
function main(): i32 {
  console.log(O.x);
  console.log(W.name);
  console.log(W.n);
  return 0;
}

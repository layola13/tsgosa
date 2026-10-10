interface P { x: i32 }
const N = {x: 1};
const O: P = {x: 2};
function m(): i32 {
  O.x = 9;
  return O.x;
}
function a(): i32 {
  const p = O;
  return p.x;
}
function main(): i32 {
  console.log(N.x);
  return m() + a();
}

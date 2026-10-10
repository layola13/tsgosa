interface P { x: i32; y: i32 }
const O: P = {x: 1, y: 2};
function f(): i32 {
  return O.x * 10 + O.y;
}
function main(): i32 {
  console.log(f());
  console.log(O.x + O.y);
  return 0;
}

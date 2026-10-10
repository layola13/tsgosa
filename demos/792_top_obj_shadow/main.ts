interface P { x: i32 }
const O: P = {x: 1};
function f(): i32 {
  const O: P = {x: 9};
  return O.x;
}
function main(): i32 {
  console.log(f());
  console.log(O.x);
  return 0;
}

const s = "hi";
function f(): i32 {
  return !s ? 1 : 0;
}
console.log(f());
function g(x: i32): void {
  console.log(x);
}
g(!s ? 1 : 0);

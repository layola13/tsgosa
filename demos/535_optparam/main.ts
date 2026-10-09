function f(o?: i32): i32 { return o ?? -1; }
function main(): i32 {
  console.log(f());
  console.log(f(4));
  return 0;
}

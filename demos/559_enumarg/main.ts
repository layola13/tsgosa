enum E {
  A,
  B,
}
function f(e: E): i32 {
  return e === E.A ? 1 : 0;
}
function main(): i32 {
  console.log(f(E.A));
  console.log(f(E.B));
  return 0;
}

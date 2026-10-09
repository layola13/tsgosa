enum E {
  A,
  B,
}
function f(d: E): i32 {
  return d;
}
function main(): i32 {
  const e: E = E.B;
  console.log(e);
  console.log(f(E.A));
  console.log(f(E.B));
  return 0;
}

function outer(x: i32): i32 {
  function inner(y: i32): i32 { return y * 2; }
  return inner(x) + 1;
}
function main(): i32 {
  console.log(outer(20));
  return 0;
}

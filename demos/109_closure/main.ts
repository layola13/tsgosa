function adder(n: i32): i32 {
  const f = (x: i32): i32 => x + n;
  return f(10);
}
function main(): i32 {
  console.log(adder(5), adder(20));
  return 0;
}

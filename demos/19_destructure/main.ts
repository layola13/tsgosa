interface Pair {
  a: i32;
  b: i32;
}
function main(): i32 {
  const [x, y] = [7, 8];
  console.log(x + y);
  const o: Pair = { a: 1, b: 2 };
  console.log(o.a * o.b);
  return 0;
}

interface P { a: i32; b: i32; }
function main(): i32 {
  const p: P = { a: 1, b: 2 };
  const { a: x, b: y } = p;
  console.log(x + y);
  const [m, n] = [1, 2];
  console.log(m + n);
  return 0;
}

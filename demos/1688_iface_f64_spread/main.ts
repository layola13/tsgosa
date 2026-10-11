interface Bx { rate: f64; n: i32; }
function main(): i32 {
  const a: Bx = { rate: 1.5, n: 1 };
  const b: Bx = { ...a, n: 2 };
  console.log(b.rate + b.n);
  return 0;
}

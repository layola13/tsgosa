interface Bx { rate: f64; }
function main(): i32 {
  const b: Bx = { rate: 2.5 };
  b.rate += 1.5;
  console.log(b.rate);
  return 0;
}

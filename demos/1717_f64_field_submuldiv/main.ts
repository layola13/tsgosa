class F { rate: f64 = 10; }
function main(): i32 {
  const f = new F();
  f.rate -= 3;
  console.log(f.rate);
  f.rate *= 2;
  console.log(f.rate);
  f.rate /= 4;
  console.log(f.rate);
  return 0;
}

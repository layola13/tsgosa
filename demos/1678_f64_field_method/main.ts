class M { rate: f64 = 2.5; get(): f64 { return this.rate; } }
function main(): i32 {
  const m = new M();
  console.log(m.get());
  return 0;
}

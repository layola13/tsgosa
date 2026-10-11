interface HasR { rate: f64; }
class C implements HasR { rate: f64 = 4.5; }
function main(): i32 {
  const c = new C();
  console.log(c.rate > 4 ? 1 : 0);
  console.log(c.rate);
  return 0;
}

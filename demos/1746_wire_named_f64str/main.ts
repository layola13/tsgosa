class F { rate: f64 = 0; s: string = ""; constructor(p: f64, t: string) { this.rate = p * 2; this.s = t + "!"; } }
function main(): i32 {
  const f = new F(1.5, "hi");
  console.log(f.rate);
  console.log(f.s);
  return 0;
}

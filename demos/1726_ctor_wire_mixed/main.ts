class F { rate: f64 = 0; n: i32 = 0; flag: boolean = false; s: string = ""; constructor() { this.rate = 3; this.n = 9; this.flag = false; this.s = "yo"; } }
function main(): i32 {
  const f = new F();
  console.log(f.rate);
  console.log(f.n);
  console.log(f.flag ? 1 : 0);
  console.log(f.s);
  return 0;
}

class P { s: string = ""; n: i32 = 0; constructor() { this.s = "x" + "y" + "z"; this.n = 20 - 4; } }
function main(): i32 {
  const p = new P();
  console.log(p.s);
  console.log(p.n);
  return 0;
}

class F { n: i32 = 0; s: string = ""; constructor(p) { this.n = p; this.s = p; } }
function main(): i32 {
  const f = new F("hi");
  console.log(f.n);
  console.log(f.s);
  return 0;
}

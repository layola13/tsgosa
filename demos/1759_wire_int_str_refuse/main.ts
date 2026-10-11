class F { s: string = ""; constructor(p) { this.s = p; } }
function main(): i32 {
  const f = new F(8);
  console.log(f.s);
  return 0;
}

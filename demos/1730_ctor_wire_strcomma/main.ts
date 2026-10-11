class F { s: string = ""; constructor() { this.s = "a,b"; } }
function main(): i32 {
  const f = new F();
  console.log(f.s);
  return 0;
}

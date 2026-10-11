class F { s: string = ""; constructor(t) { this.s = t + "!"; } }
function main(): i32 {
  const greet: string = "hey";
  const f = new F(greet);
  console.log(f.s);
  return 0;
}

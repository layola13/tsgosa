class G { s: string = ""; constructor(t: string) { this.s = t + "?"; } }
function main(): i32 {
  const greet: string = "hey";
  const g = new G(greet);
  console.log(g.s);
  console.log(greet);
  return 0;
}

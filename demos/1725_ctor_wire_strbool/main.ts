class G { s: string = ""; ok: boolean = false; constructor() { this.s = "hi"; this.ok = true; } }
function main(): i32 {
  const g = new G();
  console.log(g.s);
  console.log(g.ok ? 1 : 0);
  return 0;
}

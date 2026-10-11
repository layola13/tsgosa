class C { ok: boolean = false; constructor(k: i32) { this.ok = k != 0; } }
function main(): i32 {
  const c = new C(0);
  console.log(c.ok);
  return 0;
}

class C { ok: boolean = false; constructor(f: boolean) { this.ok = !f; } }
function main(): i32 {
  const c = new C(false);
  console.log(c.ok);
  return 0;
}

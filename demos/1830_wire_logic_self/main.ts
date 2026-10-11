class C { ok: boolean = false; constructor(f: boolean) { this.ok = f && f; } }
function main(): i32 {
  const c = new C(true);
  console.log(c.ok);
  return 0;
}

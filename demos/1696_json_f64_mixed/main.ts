interface B { ok: boolean; r: f64; n: i32; }
function main(): i32 {
  const b: B = { ok: true, r: 2.5, n: 7 };
  console.log(JSON.stringify(b));
  return 0;
}

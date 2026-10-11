class C { rate: f64 = 2.5; n: i32 = 3; }
function main(): i32 {
  const c = new C();
  console.log(JSON.stringify(c));
  return 0;
}

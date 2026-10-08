function pick(c: f64): f64 {
  return c >= 0 ? c : 0;
}
function main(): i32 {
  console.log(pick(2.5));
  console.log(pick(-1));
  return 0;
}

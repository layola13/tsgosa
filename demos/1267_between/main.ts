function between(v: i32, lo: i32, hi: i32): i32 {
  return v >= lo && v <= hi ? 1 : 0;
}
function main(): i32 {
  console.log(between(5, 0, 10));
  console.log(between(-1, 0, 10));
  console.log(between(11, 0, 10));
  return 0;
}

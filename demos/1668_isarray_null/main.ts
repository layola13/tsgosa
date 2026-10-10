function chk(v: i32[] | null): i32 { return Array.isArray(v) ? 1 : 0; }
function main(): i32 {
  console.log(chk([1]));
  console.log(chk(null));
  return 0;
}

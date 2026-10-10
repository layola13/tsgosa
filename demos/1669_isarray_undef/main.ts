function chk(v: i32[] | undefined): i32 { return Array.isArray(v) ? 1 : 0; }
function main(): i32 {
  console.log(chk([1, 2]));
  console.log(chk(undefined));
  return 0;
}

function getArr(): number[] { return [1]; }
function main(): i32 {
  const f: boolean = true;
  console.log(f == getArr());
  return 0;
}

function getS(): string { return "1"; }
function main(): i32 {
  const f: boolean = true;
  console.log(f == getS());
  return 0;
}

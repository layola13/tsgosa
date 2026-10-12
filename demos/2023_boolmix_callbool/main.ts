function isOk(): boolean { return true; }
function main(): i32 {
  const f: boolean = false;
  console.log(f == isOk());
  console.log(isOk() == f);
  return 0;
}

function getN(): number { return 1; }
function main(): i32 {
  const f: boolean = true;
  if (f == getN()) { console.log(1); } else { console.log(0); }
  return 0;
}

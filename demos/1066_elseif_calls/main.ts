function k(): i32 { return 2; }
function main(): i32 {
  if (k() === 1) { console.log(1); }
  else if (k() === 2) { console.log(2); }
  else { console.log(3); }
  return 0;
}

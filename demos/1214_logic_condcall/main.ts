function p(): i32 { return 0; }
function q(): i32 { return 5; }
function main(): i32 {
  if (p() || q()) { console.log(1); } else { console.log(0); }
  if (p() && q()) { console.log(1); } else { console.log(0); }
  return 0;
}

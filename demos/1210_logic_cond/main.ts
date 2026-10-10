function main(): i32 {
  if (3 && 4) { console.log(1); } else { console.log(0); }
  if (0 && 4) { console.log(1); } else { console.log(0); }
  if (0 || 4) { console.log(1); } else { console.log(0); }
  if (3 || 0) { console.log(1); } else { console.log(0); }
  return 0;
}

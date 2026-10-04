function main(): i32 {
  Deno.writeTextFile("/tmp/tsgosa_deno_207.txt", "hi");
  const t: string = Deno.readTextFile("/tmp/tsgosa_deno_207.txt");
  Deno.remove("/tmp/tsgosa_deno_207.txt");
  console.log(t.length);
  return 0;
}